{
  description = "git-warden: pinned gitleaks and git-everref for the tests";

  # Same release tarballs and SHA-256 as the root flake.nix, the Dockerfile
  # and scripts/install-everref.sh. Linux only: the pins are Linux binaries.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.git-tools.url = "path:../git-tools";

  outputs = { self, nixpkgs, git-tools }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      gitleaksVersion = "8.30.1";
      gitleaksAssets = {
        x86_64-linux = { arch = "x64"; hash = "sha256-VR9vyD6kV9YqDZgjfLrRBa+NVXADBR9B8+fKez8kcOs="; };
        aarch64-linux = { arch = "arm64"; hash = "sha256-5KSH7nzNfTp/fsCGV2EKo2BmN9q5JCELOu5iVw+0sIA="; };
      };
      everrefVersion = "1.0.0";
      everrefAssets = {
        x86_64-linux = { arch = "amd64"; hash = "sha256-D4e+JdiaMbI9GFHtyRd1ECPVarEJJfH+aVr9izHDOGE="; };
        aarch64-linux = { arch = "arm64"; hash = "sha256-1pRTzIxZxAjJtq6LQmdun4FePtbjBWfdlBOhsVU82+E="; };
      };
      releaseBinary = pkgs: { pname, version, url, hash, bin }:
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
        };
    in {
      devShells = nixpkgs.lib.genAttrs systems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          gl = gitleaksAssets.${system};
          er = everrefAssets.${system};
        in {
          default = pkgs.mkShell {
            inputsFrom = [ git-tools.devShells.${system}.default ];
            packages = [
              (releaseBinary pkgs {
                pname = "gitleaks";
                version = gitleaksVersion;
                url = "https://github.com/gitleaks/gitleaks/releases/download/v${gitleaksVersion}/gitleaks_${gitleaksVersion}_linux_${gl.arch}.tar.gz";
                inherit (gl) hash;
                bin = "gitleaks";
              })
              (releaseBinary pkgs {
                pname = "git-everref";
                version = everrefVersion;
                url = "https://github.com/daojyun/git-everref/releases/download/v${everrefVersion}/git-everref_${everrefVersion}_linux_${er.arch}.tar.gz";
                inherit (er) hash;
                bin = "git-everref";
              })
            ];
          };
        });
    };
}
