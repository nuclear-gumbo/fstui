{
  description = "fstui: terminal UI for managing storage (ZFS)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  # Only for staticcheck: 2026.1 in nixos-26.05 panics under Go 1.27; the
  # 2026.2 line is the first to support it.
  inputs.nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs, nixpkgs-unstable }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      unstable = nixpkgs-unstable.legacyPackages.${system};

      # nixos-26.05 defaults to Go 1.26. Analysers must be built with the same
      # Go as the code they check, or they refuse newer language versions.
      # In this nixpkgs buildGoLatestModule is still Go 1.26, so gopls,
      # govulncheck and gofumpt are rebuilt with 1.27. golangci-lint already
      # uses buildGo127Module.
      go = pkgs.go_1_27;
      buildGoModule = pkgs.buildGo127Module;

      version = self.shortRev or self.dirtyShortRev or "unknown";
    in
    {
      packages.${system} = rec {
        default = buildGoModule {
          pname = "fstui";
          inherit version;
          src = self;
          # The development repository vendors its modules; a checkout
          # without vendor/ fetches them, checked against this hash.
          vendorHash = if builtins.pathExists ./vendor then null else "sha256-+tc18Tv2BDN8/X6E7nBm8GEXHpeHUykyEgF0a0IeYdo=";
          env.CGO_ENABLED = "0";
          # buildGoModule already passes -trimpath.
          ldflags = [ "-X main.version=${version}" ];
          meta.license = pkgs.lib.licenses.gpl3Plus;
        };

        # The demo build replays recorded OpenZFS output instead of running
        # zfs and zpool (internal/demo). It is not a system package: it
        # ships only in the demo image, as fstui-demo.
        demo = default.overrideAttrs (old: {
          pname = "fstui-demo";
          tags = [ "fstui_demo" ];
          postInstall = (old.postInstall or "") + ''
            mv $out/bin/fstui $out/bin/fstui-demo
          '';
        });

        # A container to try fstui without ZFS and without risk:
        #   podman run -it --rm ghcr.io/nuclear-gumbo/fstui-demo
        # fstui runs as an unprivileged user with a writable home, so
        # Settings can save; busybox gives a shell for `podman run ... sh`.
        demo-image =
          let
            bin = pkgs.runCommand "fstui-demo-bin" { } ''
              mkdir -p $out/bin
              ln -s ${demo}/bin/fstui-demo $out/bin/fstui
            '';
          in
          pkgs.dockerTools.buildLayeredImage {
            # The registry path is chosen when the image is pushed.
            name = "fstui-demo";
            tag = version;
            contents = [ bin pkgs.busybox ];
            fakeRootCommands = ''
              mkdir -p etc home/demo/.config/fstui tmp
              printf 'root:x:0:0:root:/root:/bin/sh\ndemo:x:1000:1000:fstui demo:/home/demo:/bin/sh\n' > etc/passwd
              printf 'root:x:0:\ndemo:x:1000:\n' > etc/group
              chown -R 1000:1000 home/demo
              chmod 0755 home/demo
              chmod 1777 tmp
            '';
            config = {
              Cmd = [ "/bin/fstui" ];
              User = "1000:1000";
              WorkingDir = "/home/demo";
              Env = [ "HOME=/home/demo" "USER=demo" "TERM=xterm-256color" ];
              Labels = {
                "org.opencontainers.image.title" = "fstui demo";
                "org.opencontainers.image.description" = "fstui on recorded OpenZFS output";
                "org.opencontainers.image.source" = "https://github.com/nuclear-gumbo/fstui";
                "org.opencontainers.image.licenses" = "GPL-3.0-or-later";
                "org.opencontainers.image.version" = version;
              };
            };
          };
      };

      # ci carries only what make check needs. The Go 1.27 rebuilds are not
      # in the public binary cache, so leaving gopls and the recording tools
      # out keeps CI from compiling them on every run.
      devShells.${system} = rec {
        ci = pkgs.mkShell {
          packages = [
            go
            (pkgs.gofumpt.override { inherit buildGoModule; })
            pkgs.golangci-lint
            unstable.go-tools
            (pkgs.govulncheck.override { buildGoLatestModule = buildGoModule; })
            pkgs.shellcheck
            pkgs.git
            pkgs.gnumake
          ];
          env.CGO_ENABLED = "0";
        };
        default = ci.overrideAttrs (old: {
          nativeBuildInputs = old.nativeBuildInputs ++ [
            (pkgs.gopls.override { buildGoLatestModule = buildGoModule; })
            pkgs.vhs
            pkgs.ttyd
            pkgs.ffmpeg
            pkgs.jq
          ];
        });
      };
    };
}
