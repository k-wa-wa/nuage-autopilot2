{
  lib,
  buildGoModule,
  buildNpmPackage,
  git,
  version ? "0.1.0",
}:

let
  frontend = buildNpmPackage {
    pname = "autopilot-web";
    inherit version;

    src = lib.fileset.toSource {
      root = ../.;
      fileset = lib.fileset.unions [
        ../web/package.json
        ../web/package-lock.json
        ../web/tsconfig.json
        ../web/vite.config.ts
        ../web/index.html
        ../web/public
        ../web/src
      ];
    };

    sourceRoot = "source/web";

    npmDepsHash = "sha256-TLGX+SfA94QoOifFI4+RtcHKDCc2+CheEWLPfaI+vYY=";

    OUT_DIR = "dist";

    installPhase = ''
      runHook preInstall
      mkdir -p $out
      cp -r dist/* $out/
      runHook postInstall
    '';
  };
in
buildGoModule {
  pname = "autopilot";
  inherit version;

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      ../internal
      ../config.example.yaml
    ];
  };

  vendorHash = "sha256-8xWgUlGTyP85vEodUTcSOhs2gnwVBKBFw1vuliLM/ZM=";

  # フロントエンドの成果物を internal/web/assets に配置して embed させる
  preBuild = ''
    mkdir -p internal/web/assets
    cp -r ${frontend}/* internal/web/assets/
  '';

  CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
  ];

  nativeCheckInputs = [ git ];

  meta = {
    description = "GitHub Projects をステートマシンとする自動開発パイプラインの常駐ワーカー";
    mainProgram = "autopilot";
    platforms = lib.platforms.unix;
  };
}
