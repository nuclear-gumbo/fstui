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
          vendorHash = "sha256-+tc18Tv2BDN8/X6E7nBm8GEXHpeHUykyEgF0a0IeYdo=";
          env.CGO_ENABLED = "0";
          # buildGoModule already passes -trimpath.
          ldflags = [ "-X main.version=${version}" ];
          meta.license = pkgs.lib.licenses.gpl3Plus;
        };

        # The demo build replays recorded OpenZFS output instead of running
        # zfs and zpool (internal/demo). It is not a system package: it
        # ships in the demo image (Containerfile.demo).
        demo = default.overrideAttrs (old: {
          pname = "fstui-demo";
          tags = [ "fstui_demo" ];
          postInstall = (old.postInstall or "") + ''
            mv $out/bin/fstui $out/bin/fstui-demo
          '';
        });
      };

      # ci carries only what make check needs; default adds gopls, which
      # is rebuilt with Go 1.27 and so not in the public binary cache.
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
          ];
        });
      };
    };
}
