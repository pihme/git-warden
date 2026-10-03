{
  # Reproducible build and development environment for Git Warden.
  #
  #   nix build                  # push-guard and pull-guard in ./result/bin
  #   nix develop                # shell with Go, git, gitleaks, git-everref
  #   nix flake check            # builds and runs go test ./... in the sandbox
  #
  # gitleaks and git-everref are the same pinned releases as in CI and the
  # Dockerfile (static Linux binaries, SHA-256 fixed below), so the dev shell
  # and the hosts agree on versions. The guards never fetch them at runtime.
  description = "Git Warden: Push Guard and Pull Guard between AI agents and their Git remote";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      # Git Warden is source-available (PolyForm Noncommercial), which Nix
      # counts as unfree: allow exactly this package, nothing else.
      forAllSystems =
        f:
        nixpkgs.lib.genAttrs systems (
          system:
          f (
            import nixpkgs {
              inherit system;
              config.allowUnfreePredicate = pkg: nixpkgs.lib.getName pkg == "git-warden";
            }
          )
        );

      # Upstream release binaries: hex SHA-256 from the releases' checksums
      # (gitleaks_8.30.1_checksums.txt; scripts/install-everref.sh), as SRI here.
      gitleaksVersion = "8.30.1";
      gitleaksAssets = {
        x86_64-linux = {
          arch = "x64";
          hash = "sha256-VR9vyD6kV9YqDZgjfLrRBa+NVXADBR9B8+fKez8kcOs="; # 551f6fc8…f2470eb
        };
        aarch64-linux = {
          arch = "arm64";
          hash = "sha256-5KSH7nzNfTp/fsCGV2EKo2BmN9q5JCELOu5iVw+0sIA="; # e4a487ee…fb4b080
        };
      };
      everrefVersion = "1.0.0";
      everrefAssets = {
        x86_64-linux = {
          arch = "amd64";
          hash = "sha256-D4e+JdiaMbI9GFHtyRd1ECPVarEJJfH+aVr9izHDOGE="; # 0f87be25…b31c33861
        };
        aarch64-linux = {
          arch = "arm64";
          hash = "sha256-1pRTzIxZxAjJtq6LQmdun4FePtbjBWfdlBOhsVU82+E="; # d69453cc…1553cdbe1
        };
      };

      releaseBinary =
        pkgs:
        {
          pname,
          version,
          url,
          hash,
          bin,
          license,
          homepage,
        }:
        pkgs.stdenvNoCC.mkDerivation {
          inherit pname version;
          src = pkgs.fetchurl { inherit url hash; };
          sourceRoot = ".";
          dontConfigure = true;
          dontBuild = true;
          installPhase = ''
            runHook preInstall
            install -Dm755 ${bin} $out/bin/${bin}
            runHook postInstall
          '';
          meta = {
            inherit license homepage;
            sourceProvenance = [ pkgs.lib.sourceTypes.binaryNativeCode ];
            platforms = systems;
            mainProgram = bin;
          };
        };

      tools =
        pkgs:
        let
          system = pkgs.stdenv.hostPlatform.system;
          gl = gitleaksAssets.${system};
          er = everrefAssets.${system};
        in
        {
          gitleaks = releaseBinary pkgs {
            pname = "gitleaks";
            version = gitleaksVersion;
            url = "https://github.com/gitleaks/gitleaks/releases/download/v${gitleaksVersion}/gitleaks_${gitleaksVersion}_linux_${gl.arch}.tar.gz";
            inherit (gl) hash;
            bin = "gitleaks";
            license = pkgs.lib.licenses.mit;
            homepage = "https://github.com/gitleaks/gitleaks";
          };
          git-everref = releaseBinary pkgs {
            pname = "git-everref";
            version = everrefVersion;
            url = "https://github.com/daojyun/git-everref/releases/download/v${everrefVersion}/git-everref_${everrefVersion}_linux_${er.arch}.tar.gz";
            inherit (er) hash;
            bin = "git-everref";
            license = pkgs.lib.licenses.mit;
            homepage = "https://github.com/daojyun/git-everref";
          };
        };

      version = "0-unstable-${self.shortRev or self.dirtyShortRev or "dev"}";
    in
    {
      packages = forAllSystems (
        pkgs:
        let
          t = tools pkgs;
        in
        {
          git-warden = pkgs.buildGoModule {
            pname = "git-warden";
            inherit version;
            src = pkgs.lib.fileset.toSource {
              root = ./.;
              fileset = pkgs.lib.fileset.unions [
                ./go.mod
                ./go.sum
                ./cmd
                ./internal
                ./examples # read by the tests
                ./scripts/install-everref.sh # its pins are checked by a test
              ];
            };
            vendorHash = "sha256-g+yaVIx4jxpAQ/+WrGKxhVeliYx7nLQe/zsGpxV4Fn4=";
            subPackages = [
              "cmd/push-guard"
              "cmd/pull-guard"
            ];
            env.CGO_ENABLED = 0;
            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];
            # go test ./... with the real programs; SSH and live tests skip.
            nativeCheckInputs = [
              pkgs.git
              t.gitleaks
              t.git-everref
            ];
            # all packages, not only subPackages
            checkPhase = ''
              runHook preCheck
              export HOME=$TMPDIR GITLEAKS_REQUIRED=1 EVERREF_REQUIRED=1
              go test ./...
              runHook postCheck
            '';
            meta = {
              description = "Push Guard and Pull Guard between AI agents and their Git remote";
              license = {
                fullName = "PolyForm Noncommercial License 1.0.0";
                url = "https://polyformproject.org/licenses/noncommercial/1.0.0/";
                free = false;
                redistributable = true;
              };
              platforms = systems;
            };
          };
          default = self.packages.${pkgs.stdenv.hostPlatform.system}.git-warden;
          inherit (t) gitleaks git-everref;
        }
      );

      devShells = forAllSystems (
        pkgs:
        let
          t = tools pkgs;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.git
              pkgs.openssh # for TestSSHRemote (sshd, ssh-keygen)
              t.gitleaks
              t.git-everref
            ];
          };
        }
      );

      checks = forAllSystems (pkgs: {
        git-warden = self.packages.${pkgs.stdenv.hostPlatform.system}.git-warden;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
