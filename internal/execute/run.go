// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// DefaultReadTimeout bounds a read when the caller sets no deadline.
const DefaultReadTimeout = 30 * time.Second

// maxReadOutput bounds what a read keeps of a command's output. Real
// listings are far smaller (about 100 MiB of JSON for 100,000
// snapshots); the bound keeps a pool with millions of objects, made by
// any user with delegated create or snapshot rights, from exhausting a
// root session's memory.
const maxReadOutput = 256 << 20

// maxReadStderr bounds the error output kept from a read.
const maxReadStderr = 1 << 20

// ErrOutputTooLarge is returned when a read's output passes maxReadOutput.
var ErrOutputTooLarge = errors.New("output is larger than fstui reads")

// pipeWaitDelay bounds how long Read waits for output pipes after the
// command is killed: a child process it started can hold them open, and
// without a bound a timed-out read would still block until that child exits.
const pipeWaitDelay = time.Second

// env is the complete environment of every command. Nothing is inherited:
// variables such as ZPOOL_IMPORT_PATH or a hostile PATH would change what
// the command does.
var env = []string{
	"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
	"LC_ALL=C",
}

// Argv is a command line: a tool, its options, and its operands. Operands
// always follow "--", because zfs and zpool permute options and would read
// an operand starting with "-" as flags.
type Argv struct {
	Tool     Tool
	Options  []string
	Operands []string
	// NoSeparator leaves out "--" for the few subcommands that reject it (zfs
	// promote on 2.3.9). Such an argv is refused by Check if any operand begins
	// with "-".
	NoSeparator bool
	// Done, if set, is a message with which a non-zero exit still means
	// the change was made: an unprivileged zfs create makes the
	// filesystem, then cannot mount it and exits 1.
	Done string
	// Input, if set, is this step's standard input instead of the
	// plan's: a value written with tee. It is not a secret and is
	// shown in the confirmation.
	Input []byte
	// Stdin marks the step that reads the plan's standard input (a
	// passphrase). Other steps get none, so a passphrase reaches only
	// the command that needs it.
	Stdin bool
}

// Args returns the arguments after the program name.
func (a Argv) Args() []string {
	args := make([]string, 0, len(a.Options)+1+len(a.Operands))
	args = append(args, a.Options...)
	if len(a.Operands) > 0 {
		if !a.NoSeparator {
			args = append(args, "--")
		}
		args = append(args, a.Operands...)
	}
	return args
}

// ErrOperandOption is returned by Check for an operand that would be read
// as an option.
var ErrOperandOption = errors.New("operand begins with - and the command takes no --")

// Check refuses an argv whose operands could be read as options.
func (a Argv) Check() error {
	if !a.NoSeparator {
		return nil
	}
	for _, o := range a.Operands {
		if strings.HasPrefix(o, "-") {
			return fmt.Errorf("%s: %w", a, ErrOperandOption)
		}
	}
	return nil
}

// String renders the command for previews and logs. Arguments are quoted
// when they contain spaces or quotes, and the whole is sanitised as
// untrusted text.
func (a Argv) String() string {
	if a.Tool == SSH && len(a.Operands) > 0 {
		// The remote command is shown by Remote's errors; ssh's own
		// options would only bury it.
		return termtext.New("ssh " + a.Operands[0]).Display()
	}
	parts := []string{string(a.Tool)}
	for _, s := range a.Args() {
		if s == "" || strings.ContainsAny(s, " '\"") {
			s = fmt.Sprintf("%q", s)
		}
		parts = append(parts, s)
	}
	return termtext.New(strings.Join(parts, " ")).Display()
}

// Shell renders the command for a POSIX shell, to copy and run: every
// argument that is not made only of characters a shell leaves alone is
// put in single quotes, with any single quote in it written as '\”.
// String is for showing a command; Shell is for running it by hand.
func (a Argv) Shell() string {
	parts := []string{string(a.Tool)}
	for _, s := range a.Args() {
		parts = append(parts, shellQuote(s))
	}
	return strings.Join(parts, " ")
}

// shellSafe are the characters a POSIX shell gives no meaning to in a
// word. ZFS names may also hold spaces and ':', which are quoted.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=,./_-"

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, shellSafe) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Result is what a finished command produced.
type Result struct {
	Stdout   []byte
	Stderr   termtext.Text
	ExitCode int
	Duration time.Duration
}

// CommandError is returned when a command ran and exited non-zero.
type CommandError struct {
	Argv     Argv
	ExitCode int
	Stderr   termtext.Text
	// Host is the ssh destination the command ran on through Remote;
	// empty for a local command.
	Host string
}

// Is reports a command ssh could not run as ErrUnreachable: ssh exits
// with 255 for its own errors, but so may the remote command (zpool
// events refusing a user who is not root), so the exit status alone does
// not say which. ssh's own messages do.
func (e *CommandError) Is(target error) bool {
	return target == ErrUnreachable && (e.Host != "" || e.Argv.Tool == SSH) && e.ExitCode == sshFailure && sshFailed(e.Stderr.Raw())
}

func (e *CommandError) Error() string {
	msg := termtext.New(strings.TrimSpace(e.Stderr.Raw())).Display() // trimmed before escaping, so a CR LF ending is not shown
	if msg == "" {
		msg = "no error output"
	}
	if e.Host != "" {
		if errors.Is(e, ErrUnreachable) {
			return fmt.Sprintf("%s could not be reached: %s", e.Host, msg)
		}
		return fmt.Sprintf("%s on %s exited with status %d: %s", e.Argv, e.Host, e.ExitCode, msg)
	}
	return fmt.Sprintf("%s exited with status %d: %s", e.Argv, e.ExitCode, msg)
}

// Runner runs resolved tools.
type Runner struct {
	tools *Tools
	// ReadTimeout bounds a read whose context has no deadline;
	// DefaultReadTimeout when zero (limits.read_timeout).
	ReadTimeout time.Duration
}

// NewRunner returns a Runner for tools resolved by Resolve.
func NewRunner(tools *Tools) *Runner { return &Runner{tools: tools} }

// Read runs a command that does not change state. It may be cancelled
// through ctx, and is given DefaultReadTimeout if ctx has no deadline.
func (r *Runner) Read(ctx context.Context, a Argv) (Result, error) {
	if err := a.Check(); err != nil {
		return Result{}, err
	}
	path, ok := r.tools.Path(a.Tool)
	if !ok {
		return Result{}, fmt.Errorf("%s: %w", a.Tool, ErrNotResolved)
	}
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmp.Or(r.ReadTimeout, DefaultReadTimeout))
		defer cancel()
	}

	stdout := &limitedBuffer{max: maxReadOutput}
	stderr := &limitedBuffer{max: maxReadStderr}
	// The path was verified by Resolve and the arguments are an argv vector;
	// no shell is involved.
	cmd := exec.CommandContext(ctx, path, a.Args()...) //nolint:gosec // see above
	cmd.Env = envFor(a.Tool)
	cmd.Dir = "/"
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = pipeWaitDelay

	start := time.Now()
	err := cmd.Run()
	res := Result{
		Stdout:   stdout.Bytes(),
		Stderr:   termtext.New(stderr.String()),
		Duration: time.Since(start),
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("%s: %w after %s", a, ctxErr, res.Duration.Round(time.Millisecond))
	}
	if stdout.over {
		return Result{}, fmt.Errorf("%s: %w (%d MiB)", a, ErrOutputTooLarge, maxReadOutput>>20)
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		return res, &CommandError{Argv: a, ExitCode: res.ExitCode, Stderr: res.Stderr}
	default:
		return res, fmt.Errorf("starting %s: %w", a, err)
	}
}

// ReadTo runs a command that does not change state, like Read, but hands
// its output to consume as it arrives instead of keeping it: a listing of
// many snapshots is parsed while zfs prints it, never held whole. The
// deadline, the output limit and the errors are Read's. If consume
// returns an error, the command is stopped and that error returned.
func (r *Runner) ReadTo(ctx context.Context, a Argv, consume func(io.Reader) error) error {
	if err := a.Check(); err != nil {
		return err
	}
	path, ok := r.tools.Path(a.Tool)
	if !ok {
		return fmt.Errorf("%s: %w", a.Tool, ErrNotResolved)
	}
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmp.Or(r.ReadTimeout, DefaultReadTimeout))
		defer cancel()
	}
	cmdCtx, stop := context.WithCancel(ctx) // stops the command when consume gives up
	defer stop()
	stderr := &limitedBuffer{max: maxReadStderr}
	cmd := exec.CommandContext(cmdCtx, path, a.Args()...) //nolint:gosec // as in Read
	cmd.Env = envFor(a.Tool)
	cmd.Dir = "/"
	cmd.Stderr = stderr
	cmd.WaitDelay = pipeWaitDelay
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("starting %s: %w", a, err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", a, err)
	}
	limited := &limitedReader{r: out, left: maxReadOutput}
	consumeErr := consume(limited)
	if consumeErr == nil {
		if _, err := io.Copy(io.Discard, limited); err != nil { // anything after what consume read
			consumeErr = fmt.Errorf("reading %s: %w", a, err)
		}
	}
	if consumeErr != nil {
		stop() // nobody reads the rest
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case limited.over:
		return fmt.Errorf("%s: %w (%d MiB)", a, ErrOutputTooLarge, maxReadOutput>>20)
	case ctx.Err() != nil: // the deadline, or the caller cancelled
		return fmt.Errorf("%s: %w after %s", a, ctx.Err(), time.Since(start).Round(time.Millisecond))
	case consumeErr != nil:
		return consumeErr
	case errors.As(err, &exitErr):
		return &CommandError{Argv: a, ExitCode: exitErr.ExitCode(), Stderr: termtext.New(stderr.String())}
	case err != nil:
		return fmt.Errorf("running %s: %w", a, err)
	}
	return nil
}

// limitedReader reads at most left bytes, then reports an error and sets
// over.
type limitedReader struct {
	r    io.Reader
	left int
	over bool
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left <= 0 {
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			l.over = true
			return 0, ErrOutputTooLarge
		}
		return 0, io.EOF
	}
	p = p[:min(len(p), l.left)]
	n, err := l.r.Read(p)
	l.left -= n
	return n, err //nolint:wrapcheck // io.Reader passes errors through
}

// maxStreamLine bounds one line of a streamed command's output; a longer
// line ends the stream with an error instead of growing without limit.
const maxStreamLine = 64 << 10

// maxStderr bounds the error output kept from a streamed command.
const maxStderr = 64 << 10

// Stream runs a command that does not change state and keeps printing,
// such as zpool iostat with an interval, and calls line with each line of
// its output as it arrives, from the calling goroutine. It has no
// timeout: it runs until ctx is cancelled, which kills the command and
// returns nil, or until the command exits. line must not keep its
// argument, which is reused.
func (r *Runner) Stream(ctx context.Context, a Argv, line func([]byte)) error {
	if err := a.Check(); err != nil {
		return err
	}
	path, ok := r.tools.Path(a.Tool)
	if !ok {
		return fmt.Errorf("%s: %w", a.Tool, ErrNotResolved)
	}
	cmd := exec.CommandContext(ctx, path, a.Args()...) //nolint:gosec // as in Read
	cmd.Env = envFor(a.Tool)
	cmd.Dir = "/"
	stderr := &limitedBuffer{max: maxStderr}
	cmd.Stderr = stderr
	cmd.WaitDelay = pipeWaitDelay
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("starting %s: %w", a, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", a, err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 4096), maxStreamLine)
	for sc.Scan() {
		line(sc.Bytes())
	}
	scanErr := sc.Err()
	if scanErr != nil {
		_ = cmd.Process.Kill() // stop a command whose output nobody reads any more
	}
	err = cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return nil //nolint:nilerr // cancelling is how a stream is meant to end
	case scanErr != nil:
		return fmt.Errorf("reading %s: %w", a, scanErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &CommandError{Argv: a, ExitCode: exitErr.ExitCode(), Stderr: termtext.New(stderr.String())}
	}
	if err != nil {
		return fmt.Errorf("running %s: %w", a, err)
	}
	return nil
}

// limitedBuffer keeps the first max bytes written to it; over says
// whether more was written.
type limitedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.Len()
	if len(p) > room {
		b.over = true
	}
	if room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
