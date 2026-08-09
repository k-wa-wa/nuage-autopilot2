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
