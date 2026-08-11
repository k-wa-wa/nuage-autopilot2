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

  # ビルドに要るものだけを含める。ドキュメントを直しても再ビルドされない。
  # config.example.yaml は internal/config のテストが読むため必須。
  #
  # internal/web/assets/dist は必ず除く。作業ツリーが dirty なとき、flake は
  # 追跡外のファイルも含めてディレクトリごとコピーするため、手元の
  # npm run build の成果物が source に入り込み、derivation のハッシュが
  # 手元の状態で変わる（古いハッシュ付きバンドルも一緒に埋め込まれる）。
  # 中身は下の preBuild で改めて入れる。
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      (lib.fileset.difference ../internal (lib.fileset.maybeMissing ../internal/web/assets/dist))
      ../config.example.yaml
    ];
  };

  vendorHash = "sha256-8xWgUlGTyP85vEodUTcSOhs2gnwVBKBFw1vuliLM/ZM=";

  # subPackages は指定しない。指定すると checkPhase の go test も
  # そのパッケージだけになり、internal/ のテストが走らなくなる。
  # main パッケージは cmd/autopilot だけなので、生成されるバイナリは 1 つ。

  # フロントエンドの成果物を internal/web/assets/dist に配置して embed させる。
  # src から除いてあるので、ここに置かれるのは常にこのビルドの出力だけである。
  preBuild = ''
    mkdir -p internal/web/assets/dist
    cp -r ${frontend}/* internal/web/assets/dist/
  '';

  # SQLite ドライバは pure Go（modernc.org/sqlite）なので cgo は不要。
  # cgo 版に差し替えるとクロスコンパイルとこのビルドが壊れる。
  # buildGoModule が CGO_ENABLED を derivation 引数として渡すため、env: では指定しない。
  CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
  ];

  # workspace / engine のテストは実際の git を起動する。
  nativeCheckInputs = [ git ];

  meta = {
    description = "GitHub Projects をステートマシンとする自動開発パイプラインの常駐ワーカー";
    mainProgram = "autopilot";
    platforms = lib.platforms.unix;
  };
}
