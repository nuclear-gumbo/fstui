// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// Cat and Ls read files and list directories on a remote host, for the
// readers that read the local machine's files directly (kstats, module
// parameters, Debian's scrub cron file). The probe checks them like zfs.
const (
	Cat Tool = "cat"
	Ls  Tool = "ls"
)

// ReadFile reads a file on the host. A file that is not there is
// fs.ErrNotExist, as os.ReadFile reports it locally.
func (r *Remote) ReadFile(path string) ([]byte, error) {
	res, err := r.Read(context.Background(), Argv{Tool: Cat, Operands: []string{path}})
	if err != nil {
		return nil, notExist(err)
	}
	return res.Stdout, nil
}

// ReadDir lists the names in a directory on the host, as the readers'
// ReadDir seam takes them.
func (r *Remote) ReadDir(path string) ([]string, error) {
	res, err := r.Read(context.Background(), Argv{Tool: Ls, Options: []string{"-1A"}, Operands: []string{path}})
	if err != nil {
		return nil, notExist(err)
	}
	return strings.Split(strings.TrimSuffix(string(res.Stdout), "\n"), "\n"), nil
}

// notExist marks cat's and ls's report of a missing path, in the C
// locale every remote command runs in, as fs.ErrNotExist.
func notExist(err error) error {
	var ce *CommandError
	if errors.As(err, &ce) && strings.Contains(ce.Stderr.Raw(), "No such file or directory") {
		return fmt.Errorf("%w: %w", fs.ErrNotExist, err)
	}
	return err
}
