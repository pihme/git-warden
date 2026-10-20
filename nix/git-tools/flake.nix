{
  description = "git-warden: git and OpenSSH for the tests";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.reference-env.url = "path:../reference-env";

  outputs = { self, nixpkgs, reference-env }:
    let
      systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin"];
      forAll = f: nixpkgs.lib.genAttrs systems (system:
        f system nixpkgs.legacyPackages.${system});
    in {
      devShells = forAll (system: pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [ git openssh ];
            inputsFrom = [ reference-env.devShells.${system}.default ];
        };
      });
    };
}
