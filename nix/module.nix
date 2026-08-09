# autopilot を systemd の常駐サービスとして動かす NixOS モジュール。
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.autopilot;
  yamlFormat = pkgs.formats.yaml { };

  configFile =
    if cfg.configFile != null then
      cfg.configFile
    else
      yamlFormat.generate "autopilot-config.yaml" cfg.settings;
in
{
  options.services.autopilot = {
    enable = lib.mkEnableOption "autopilot（自動開発パイプラインの常駐ワーカー）";

    package = lib.mkOption {
      type = lib.types.package;
      description = "使用する autopilot パッケージ。";
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "autopilot";
      description = ''
        サービスを実行するユーザー。

        コーディングエージェント CLI（claude 等）をユーザーのホームに入れている場合は、
        そのユーザーを指定し {option}`services.autopilot.createUser` を false にする。
      '';
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = cfg.user;
      defaultText = lib.literalExpression "config.services.autopilot.user";
      description = "サービスを実行するグループ。";
    };

    createUser = lib.mkOption {
      type = lib.types.bool;
      default = cfg.user == "autopilot";
      defaultText = lib.literalExpression ''config.services.autopilot.user == "autopilot"'';
      description = "ユーザーとグループをこのモジュールで作成するかどうか。";
    };

    stateDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/autopilot";
      description = ''
        DB・ワークスペース・エージェントのログを置くディレクトリ。

        /var/lib 配下なら systemd の StateDirectory で作成・権限設定される。
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/var/lib/autopilot/secrets.env";
      description = ''
        GH_TOKEN を渡すための EnvironmentFile。

        Projects v2 を操作するため classic PAT の project スコープが必要。
        **Nix ストアに置かないこと**（誰でも読める）。sops-nix 等で配置したパスを指定する。

        ```
        GH_TOKEN=ghp_xxxxxxxxxxxx
        ```
      '';
    };

    extraPackages = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = [ ];
      example = lib.literalExpression "[ pkgs.nodejs_22 pkgs.python3 ]";
      description = ''
        PATH に追加するパッケージ。

        git と gh は常に含まれる。対象リポジトリのテストやビルドに必要なツールをここに足す。
      '';
    };

    extraPath = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "/home/nixos/.local" ];
      description = ''
        PATH に追加する生のディレクトリ。

        Nix 管理外のコーディングエージェント CLI（claude 等）を置いている場所を指定する。
        systemd の path= は `<dir>/bin` を PATH に加えるため、`/home/nixos/.local` を
        指定すると `/home/nixos/.local/bin` が通る。
      '';
    };

    configFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = ''
        設定ファイルを直接指定する場合のパス。
        指定すると {option}`services.autopilot.settings` は無視される。
      '';
    };

    settings = lib.mkOption {
      type = yamlFormat.type;
      default = { };
      example = lib.literalExpression ''
        {
          project = {
            owner = "k-wa-wa";
            number = 1;
            owner_type = "user";
          };
          repos = [
            { owner = "k-wa-wa"; name = "example-repo"; }
          ];
        }
      '';
      description = ''
        config.yaml の内容。workspace / database は stateDir 配下の既定値が入る。

        トークンはここに書かないこと（Nix ストアに平文で残る）。
        {option}`services.autopilot.environmentFile` を使う。
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.configFile != null || cfg.settings ? project;
        message = "services.autopilot.settings.project を指定するか、configFile を指定してください。";
      }
      {
        assertion = cfg.configFile != null || cfg.settings ? repos;
        message = "services.autopilot.settings.repos を指定するか、configFile を指定してください。";
      }
    ];

    services.autopilot.settings = {
      workspace = lib.mkDefault "${cfg.stateDir}/workspaces";
      database = lib.mkDefault "${cfg.stateDir}/autopilot.db";
    };

    users.users = lib.mkIf cfg.createUser {
      ${cfg.user} = {
        isSystemUser = true;
        group = cfg.group;
        home = cfg.stateDir;
        description = "autopilot service user";
      };
    };

    users.groups = lib.mkIf (cfg.createUser && cfg.group == cfg.user) {
      ${cfg.group} = { };
    };

    systemd.services.autopilot = {
      description = "autopilot - 自動開発パイプラインの常駐ワーカー";

      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      # エージェントは対象リポジトリのテストやビルドを実行するため、
      # 必要なツールが PATH に無いと Blocked が多発する。
      path =
        [
          pkgs.git
          pkgs.gh
        ]
        ++ cfg.extraPackages
        ++ cfg.extraPath;

      serviceConfig = {
        Type = "simple";
        ExecStart = "${lib.getExe cfg.package} run --config ${configFile}";

        User = cfg.user;
        Group = cfg.group;

        Restart = "always";
        RestartSec = "10s";

        # 実行中のエージェントに猶予を与えて終了させるため長めに取る。
        TimeoutStopSec = "5m";

        StateDirectory = lib.mkIf (cfg.stateDir == "/var/lib/autopilot") "autopilot";
        WorkingDirectory = cfg.stateDir;

        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;

        # 注意: サンドボックス系のオプションは意図的に最小限にしている。
        # このサービスは対象リポジトリの任意のコードをビルド・テストとして実行するため、
        # ProtectHome や PrivateDevices を強めると正常な作業まで失敗する。
        # 隔離が必要な場合はホスト自体を専用に分けること。
        NoNewPrivileges = false;
      };
    };
  };
}
