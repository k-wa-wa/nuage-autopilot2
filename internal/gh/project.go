package gh

import (
	"context"
	"fmt"
)

// Project は Projects v2 のメタ情報（ID と Status フィールドの選択肢）。
type Project struct {
	ID            string
	StatusFieldID string
	StatusField   string
	// Options は選択肢名 -> optionID。
	Options map[string]string
}

// OptionID は Status 名に対応する選択肢 ID を返す。
func (p *Project) OptionID(status string) (string, error) {
	id, ok := p.Options[status]
	if !ok {
		known := make([]string, 0, len(p.Options))
		for k := range p.Options {
			known = append(known, k)
		}
		return "", fmt.Errorf("Status %q が Project に存在しません（定義済み: %v）", status, known)
	}
	return id, nil
}

// ProjectItem は Project 上の 1 カード。Issue 以外（PR や Draft）は IssueNumber が 0 になる。
type ProjectItem struct {
	ItemID      string
	IssueNumber int
	Repo        string
	Title       string
	Status      string
	State       string // OPEN / CLOSED
	StateReason string // COMPLETED / NOT_PLANNED / REOPENED
	Archived    bool
	TypeName    string
}

// IsIssue は Issue のカードかどうかを返す。
func (i ProjectItem) IsIssue() bool { return i.TypeName == "Issue" && i.IssueNumber > 0 }

// IsClosed は Issue がクローズ済みかを返す。
func (i ProjectItem) IsClosed() bool { return i.State == "CLOSED" }

const projectQueryTmpl = `
query($login: String!, $number: Int!, $field: String!) {
  %s(login: $login) {
    projectV2(number: $number) {
      id
      field(name: $field) {
        ... on ProjectV2SingleSelectField {
          id
          name
          options { id name }
        }
      }
    }
  }
}`

// LoadProject は Project の ID と Status フィールド定義を取得する。
func (c *Client) LoadProject(ctx context.Context, ownerType, login string, number int, fieldName string) (*Project, error) {
	root := "user"
	if ownerType == "organization" {
		root = "organization"
	}
	query := fmt.Sprintf(projectQueryTmpl, root)

	var resp map[string]struct {
		ProjectV2 *struct {
			ID    string `json:"id"`
			Field *struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Options []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"options"`
			} `json:"field"`
		} `json:"projectV2"`
	}
	err := c.graphql(ctx, query, map[string]any{
		"login": login, "number": number, "field": fieldName,
	}, &resp)
	if err != nil {
		return nil, err
	}
	owner, ok := resp[root]
	if !ok || owner.ProjectV2 == nil {
		return nil, fmt.Errorf("Project %s/%d が見つかりません（owner_type: %s）", login, number, ownerType)
	}
	if owner.ProjectV2.Field == nil {
		return nil, fmt.Errorf("Project にフィールド %q（単一選択）がありません", fieldName)
	}
	p := &Project{
		ID:            owner.ProjectV2.ID,
		StatusFieldID: owner.ProjectV2.Field.ID,
		StatusField:   owner.ProjectV2.Field.Name,
		Options:       map[string]string{},
	}
	for _, o := range owner.ProjectV2.Field.Options {
		p.Options[o.Name] = o.ID
	}
	return p, nil
}

const itemsQuery = `
query($id: ID!, $cursor: String, $field: String!) {
  node(id: $id) {
    ... on ProjectV2 {
      items(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          isArchived
          fieldValueByName(name: $field) {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          content {
            __typename
            ... on Issue {
              number
              title
              state
              stateReason
              repository { name owner { login } }
            }
          }
        }
      }
    }
  }
}`

// ListItems は Project の全カードを Status のみ射影して取得する。
// Issue 本文もコメントも取らないため軽量（100 件で 1 リクエスト）。
func (c *Client) ListItems(ctx context.Context, p *Project) ([]ProjectItem, error) {
	var out []ProjectItem
	var cursor *string
	for {
		var resp struct {
			Node struct {
				Items struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID         string `json:"id"`
						IsArchived bool   `json:"isArchived"`
						FieldValue *struct {
							Name string `json:"name"`
						} `json:"fieldValueByName"`
						Content *struct {
							TypeName    string `json:"__typename"`
							Number      int    `json:"number"`
							Title       string `json:"title"`
							State       string `json:"state"`
							StateReason string `json:"stateReason"`
							Repository  struct {
								Name  string `json:"name"`
								Owner struct {
									Login string `json:"login"`
								} `json:"owner"`
							} `json:"repository"`
						} `json:"content"`
					} `json:"nodes"`
				} `json:"items"`
			} `json:"node"`
		}
		vars := map[string]any{"id": p.ID, "field": p.StatusField}
		if cursor != nil {
			vars["cursor"] = *cursor
		}
		if err := c.graphql(ctx, itemsQuery, vars, &resp); err != nil {
			return nil, err
		}
		for _, n := range resp.Node.Items.Nodes {
			it := ProjectItem{ItemID: n.ID, Archived: n.IsArchived}
			if n.FieldValue != nil {
				it.Status = n.FieldValue.Name
			}
			if n.Content != nil {
				it.TypeName = n.Content.TypeName
				it.IssueNumber = n.Content.Number
				it.Title = n.Content.Title
				it.State = n.Content.State
				it.StateReason = n.Content.StateReason
				if n.Content.Repository.Name != "" {
					it.Repo = n.Content.Repository.Owner.Login + "/" + n.Content.Repository.Name
				}
			}
			out = append(out, it)
		}
		if !resp.Node.Items.PageInfo.HasNextPage {
			break
		}
		end := resp.Node.Items.PageInfo.EndCursor
		cursor = &end
	}
	return out, nil
}

const setStatusMutation = `
mutation($project: ID!, $item: ID!, $field: ID!, $option: String!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $project, itemId: $item, fieldId: $field,
    value: { singleSelectOptionId: $option }
  }) { projectV2Item { id } }
}`

// SetStatus はカードの Status を変更する。
func (c *Client) SetStatus(ctx context.Context, p *Project, itemID, status string) error {
	optID, err := p.OptionID(status)
	if err != nil {
		return err
	}
	return c.graphql(ctx, setStatusMutation, map[string]any{
		"project": p.ID, "item": itemID, "field": p.StatusFieldID, "option": optID,
	}, nil)
}

// ProjectInfo は作成された Project の基本情報。
type ProjectInfo struct {
	ID     string
	Number int
	URL    string
	Title  string
}

// SingleSelectOptionInput は Single Select フィールドの選択肢入力。
//
// ID は「既存の選択肢を更新する」ことを表す。省略すると新規の選択肢として作られ、
// 元の選択肢は削除されるため、その値が入っていたカードの Status が消える。
// Name / Color / Description はスキーマ上すべて必須なので omitempty は付けない。
type SingleSelectOptionInput struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// SingleSelectOption は Project から読み出した既存の選択肢。
type SingleSelectOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// DefaultStatuses は autopilot が標準で要求する 7 つのステータス定義（カラー付き）。
var DefaultStatuses = []SingleSelectOptionInput{
	{Name: "📥 Inbox", Color: "GRAY", Description: "新規要求の受付・仕様対話中"},
	{Name: "🎯 Ready", Color: "BLUE", Description: "人間が実装着手を承認（唯一の手動操作）"},
	{Name: "🚧 In Progress", Color: "YELLOW", Description: "エージェントが実装・テスト・PR作成中"},
	{Name: "🔍 Verifying", Color: "PURPLE", Description: "CI実行待ち・セルフレビュー中"},
	{Name: "👀 In Review", Color: "ORANGE", Description: "人間のレビュー待ち"},
	{Name: "⏸ Blocked", Color: "RED", Description: "要人間介入（助言コメントで再開）"},
	{Name: "✅ Done", Color: "GREEN", Description: "完了・クローズ"},
}

// ResolveOwnerID は user または organization の GraphQL Node ID を取得する。
func (c *Client) ResolveOwnerID(ctx context.Context, ownerType, login string) (string, error) {
	root := "user"
	if ownerType == "organization" {
		root = "organization"
	}
	query := fmt.Sprintf(`query($login: String!) { %s(login: $login) { id } }`, root)
	var resp map[string]*struct {
		ID string `json:"id"`
	}
	if err := c.graphql(ctx, query, map[string]any{"login": login}, &resp); err != nil {
		return "", err
	}
	owner := resp[root]
	if owner == nil || owner.ID == "" {
		return "", fmt.Errorf("オーナー %q (%s) の Node ID を取得できませんでした", login, ownerType)
	}
	return owner.ID, nil
}

const createProjectMutation = `
mutation($ownerId: ID!, $title: String!) {
  createProjectV2(input: { ownerId: $ownerId, title: $title }) {
    projectV2 { id number url title }
  }
}`

const projectFieldsQuery = `
query($id: ID!, $cursor: String) {
  node(id: $id) {
    ... on ProjectV2 {
      fields(first: 50, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          ... on ProjectV2SingleSelectField {
            id
            name
            options { id name color description }
          }
        }
      }
    }
  }
}`

const updateFieldMutation = `
mutation($fieldId: ID!, $name: String!, $options: [ProjectV2SingleSelectFieldOptionInput!]!) {
  updateProjectV2Field(input: {
    fieldId: $fieldId,
    name: $name,
    singleSelectOptions: $options
  }) {
    projectV2Field {
      ... on ProjectV2SingleSelectField { id name }
    }
  }
}`

const createFieldMutation = `
mutation($projectId: ID!, $name: String!, $options: [ProjectV2SingleSelectFieldOptionInput!]!) {
  createProjectV2Field(input: {
    projectId: $projectId,
    dataType: SINGLE_SELECT,
    name: $name,
    singleSelectOptions: $options
  }) {
    projectV2Field {
      ... on ProjectV2SingleSelectField { id name }
    }
  }
}`

// GetProjectID は指定された Project 番号の Node ID を取得する。
func (c *Client) GetProjectID(ctx context.Context, ownerType, login string, number int) (string, error) {
	root := "user"
	if ownerType == "organization" {
		root = "organization"
	}
	query := fmt.Sprintf(`query($login: String!, $number: Int!) { %s(login: $login) { projectV2(number: $number) { id } } }`, root)
	var resp map[string]*struct {
		ProjectV2 *struct {
			ID string `json:"id"`
		} `json:"projectV2"`
	}
	if err := c.graphql(ctx, query, map[string]any{"login": login, "number": number}, &resp); err != nil {
		return "", err
	}
	owner := resp[root]
	if owner == nil || owner.ProjectV2 == nil || owner.ProjectV2.ID == "" {
		return "", fmt.Errorf("Project %s/%d が見つかりません（owner_type: %s）", login, number, ownerType)
	}
	return owner.ProjectV2.ID, nil
}

// mergeOptions は既存の選択肢を保持したまま、要求された選択肢を揃えた入力を作る。
//
// updateProjectV2Field の singleSelectOptions は全置換で、id を渡さない選択肢は
// 別物として作り直される。そのため
//   - 同名の既存選択肢には id を引き継ぐ（カードの Status 値が消えるのを防ぐ）
//   - 要求に無い既存選択肢も末尾に残す（消すとその値のカードが空になる）
//
// という 2 点を守る必要がある。
func mergeOptions(existing []SingleSelectOption, want []SingleSelectOptionInput) []SingleSelectOptionInput {
	byName := make(map[string]SingleSelectOption, len(existing))
	for _, o := range existing {
		byName[o.Name] = o
	}
	wanted := make(map[string]bool, len(want))

	out := make([]SingleSelectOptionInput, 0, len(want)+len(existing))
	for _, w := range want {
		wanted[w.Name] = true
		if o, ok := byName[w.Name]; ok {
			w.ID = o.ID
		}
		out = append(out, w)
	}
	for _, o := range existing {
		if wanted[o.Name] {
			continue
		}
		out = append(out, SingleSelectOptionInput(o))
	}
	return out
}

// findSingleSelectField は指定名の単一選択フィールドとその選択肢を探す。
//
// 見つからない場合は空文字を返す（呼び出し側でフィールドを新規作成する）。
// フィールド数が 50 を超える Project でも取りこぼさないようページングする。
func (c *Client) findSingleSelectField(ctx context.Context, projectID, fieldName string) (string, []SingleSelectOption, error) {
	var cursor *string
	for {
		var resp struct {
			Node *struct {
				Fields struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID      string               `json:"id"`
						Name    string               `json:"name"`
						Options []SingleSelectOption `json:"options"`
					} `json:"nodes"`
				} `json:"fields"`
			} `json:"node"`
		}
		vars := map[string]any{"id": projectID}
		if cursor != nil {
			vars["cursor"] = *cursor
		}
		if err := c.graphql(ctx, projectFieldsQuery, vars, &resp); err != nil {
			return "", nil, fmt.Errorf("フィールド一覧の取得に失敗: %w", err)
		}
		if resp.Node == nil {
			return "", nil, nil
		}
		for _, f := range resp.Node.Fields.Nodes {
			if f.Name == fieldName {
				return f.ID, f.Options, nil
			}
		}
		if !resp.Node.Fields.PageInfo.HasNextPage {
			return "", nil, nil
		}
		end := resp.Node.Fields.PageInfo.EndCursor
		cursor = &end
	}
}

// ConfigureProjectStatuses は既存の GitHub Projects v2 に対して Status 選択肢を設定・修復する。
//
// 既存の選択肢は名前が一致すれば id ごと引き継ぎ、定義外の選択肢も残す。
// カードに入っている Status を失わせないための措置（mergeOptions を参照）。
func (c *Client) ConfigureProjectStatuses(ctx context.Context, projectID, fieldName string, options []SingleSelectOptionInput) error {
	return c.configureStatuses(ctx, projectID, fieldName, options, true)
}

// configureStatuses は preserveExisting が true のとき既存選択肢を保持する。
//
// 新規作成直後の Project はカードが 0 件で、既定の Todo / In Progress / Done を
// 残す意味が無いため、作成経路だけは false で呼んで置き換える。
func (c *Client) configureStatuses(ctx context.Context, projectID, fieldName string, options []SingleSelectOptionInput, preserveExisting bool) error {
	if len(options) == 0 {
		options = DefaultStatuses
	}

	// 1. 対象フィールドと、その既存の選択肢を探す
	targetFieldID, existing, err := c.findSingleSelectField(ctx, projectID, fieldName)
	if err != nil {
		return err
	}

	// 2. Status フィールドの選択肢を更新、または新規作成
	if targetFieldID != "" {
		if preserveExisting {
			options = mergeOptions(existing, options)
		}
		vars := map[string]any{
			"fieldId": targetFieldID,
			"name":    fieldName,
			"options": options,
		}
		if err := c.graphql(ctx, updateFieldMutation, vars, nil); err != nil {
			return fmt.Errorf("フィールド %q の更新に失敗: %w", fieldName, err)
		}
	} else {
		vars := map[string]any{
			"projectId": projectID,
			"name":      fieldName,
			"options":   options,
		}
		if err := c.graphql(ctx, createFieldMutation, vars, nil); err != nil {
			return fmt.Errorf("フィールド %q の作成に失敗: %w", fieldName, err)
		}
	}
	return nil
}

// CreateProjectWithStatuses は GitHub Projects v2 を新規作成し、autopilot 向けの Status 選択肢を設定する。
func (c *Client) CreateProjectWithStatuses(ctx context.Context, ownerType, login, title, fieldName string, options []SingleSelectOptionInput) (*ProjectInfo, error) {
	if len(options) == 0 {
		options = DefaultStatuses
	}
	ownerID, err := c.ResolveOwnerID(ctx, ownerType, login)
	if err != nil {
		return nil, err
	}

	// 1. Project v2 を新規作成
	var createResp struct {
		CreateProjectV2 struct {
			ProjectV2 struct {
				ID     string `json:"id"`
				Number int    `json:"number"`
				URL    string `json:"url"`
				Title  string `json:"title"`
			} `json:"projectV2"`
		} `json:"createProjectV2"`
	}
	if err := c.graphql(ctx, createProjectMutation, map[string]any{"ownerId": ownerID, "title": title}, &createResp); err != nil {
		return nil, fmt.Errorf("Project の作成に失敗: %w", err)
	}
	proj := createResp.CreateProjectV2.ProjectV2
	info := &ProjectInfo{
		ID:     proj.ID,
		Number: proj.Number,
		URL:    proj.URL,
		Title:  proj.Title,
	}

	// 2. Status フィールドの選択肢を設定
	//    作成直後でカードが 0 件なので、既定の Todo / In Progress / Done は残さず置き換える。
	if err := c.configureStatuses(ctx, info.ID, fieldName, options, false); err != nil {
		return info, err
	}

	return info, nil
}
