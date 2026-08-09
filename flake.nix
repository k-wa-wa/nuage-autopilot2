{
  description = "GitHub Projects をステートマシンとする自動開発パイプラインの常駐ワーカー";

  # 配布先（nuage-cluster）が nixos-24.11 を pin しており、
  # inputs.nixpkgs.follows で上書きされる前提。go.mod の go ディレクティブを
  # 1.23.0 に保っているのはこのため（AGENT.md 参照）。
  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-24.11";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        autopilot = pkgs.callPackage ./nix/package.nix { };
        default = autopilot;
      });

      overlays.default = final: _prev: {
        autopilot = final.callPackage ./nix/package.nix { };
      };

      # 利用側は import するだけでよい。package の既定値をこの flake から注入する。
      nixosModules.autopilot =
        { pkgs, lib, ... }:
        {
          imports = [ ./nix/module.nix ];
          services.autopilot.package =
            lib.mkDefault
              self.packages.${pkgs.stdenv.hostPlatform.system}.autopilot;
        };
      nixosModules.default = self.nixosModules.autopilot;

      apps = forAllSystems (pkgs: rec {
        autopilot = {
          type = "app";
          program = nixpkgs.lib.getExe self.packages.${pkgs.stdenv.hostPlatform.system}.autopilot;
        };
        default = autopilot;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            golangci-lint
            git
            gh
            sqlite
          ];
        };
      });

      checks = forAllSystems (pkgs: {
        # buildGoModule の checkPhase が go test ./... を実行する。
        package = self.packages.${pkgs.stdenv.hostPlatform.system}.autopilot;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-rfc-style);
    };
}
