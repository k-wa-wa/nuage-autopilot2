{
  lib,
  buildGoModule,
  git,
  version ? "0.1.0",
}:

buildGoModule {
  pname = "autopilot";
  inherit version;

  # ビルドに要るものだけを含める。ドキュメントを直しても再ビルドされない。
  # config.example.yaml は internal/config のテストが読むため必須。
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

  # subPackages は指定しない。指定すると checkPhase の go test も
  # そのパッケージだけになり、internal/ のテストが走らなくなる。
  # main パッケージは cmd/autopilot だけなので、生成されるバイナリは 1 つ。

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
