package langfuseclient

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompileChat(t *testing.T) {
	tests := []struct {
		name         string
		template     []ChatPromptItem
		variables    map[string]string
		placeholders map[string][]ChatMessage
		want         []ChatMessage
		wantError    string
	}{
		{
			name: "variables_and_history_in_order",
			template: []ChatPromptItem{
				{Role: "system", Content: "你是 {{service_name}} 的办事助手。"},
				{Type: "placeholder", Name: "chat_history"},
				{Type: "message", Role: "user", Content: "{{question}}"},
			},
			variables: map[string]string{"service_name": "政务服务中心", "question": "我叫什么？"},
			placeholders: map[string][]ChatMessage{"chat_history": {
				{Role: "user", Content: "我叫小王。"},
				{Role: "assistant", Content: "你好，小王。"},
			}},
			want: []ChatMessage{
				{Role: "system", Content: "你是 政务服务中心 的办事助手。"},
				{Role: "user", Content: "我叫小王。"},
				{Role: "assistant", Content: "你好，小王。"},
				{Role: "user", Content: "我叫什么？"},
			},
		},
		{
			name: "empty_template", want: []ChatMessage{},
		},
		{
			name:     "nil_arguments_without_placeholders",
			template: []ChatPromptItem{{Role: "assistant", Content: "固定正文"}},
			want:     []ChatMessage{{Role: "assistant", Content: "固定正文"}},
		},
		{
			name:      "empty_string_variable_and_repeated_variable",
			template:  []ChatPromptItem{{Role: "user", Content: "[{{ name }}][{{name}}]"}},
			variables: map[string]string{"name": ""},
			want:      []ChatMessage{{Role: "user", Content: "[][]"}},
		},
		{
			name:      "inserted_values_are_literal",
			template:  []ChatPromptItem{{Role: "user", Content: "{{value}}"}},
			variables: map[string]string{"value": `{{other}} $1 \ literal`},
			want:      []ChatMessage{{Role: "user", Content: `{{other}} $1 \ literal`}},
		},
		{
			name:         "history_is_not_a_template",
			template:     []ChatPromptItem{{Type: "placeholder", Name: "history"}},
			placeholders: map[string][]ChatMessage{"history": {{Role: "user", Content: "历史 {{question}} 和未闭合 {{"}}},
			want:         []ChatMessage{{Role: "user", Content: "历史 {{question}} 和未闭合 {{"}},
		},
		{
			name:         "nil_history_is_explicitly_empty",
			template:     []ChatPromptItem{{Type: "placeholder", Name: "history"}},
			placeholders: map[string][]ChatMessage{"history": nil},
			want:         []ChatMessage{},
		},
		{
			name:         "empty_history_is_allowed",
			template:     []ChatPromptItem{{Type: "placeholder", Name: "history"}},
			placeholders: map[string][]ChatMessage{"history": {}},
			want:         []ChatMessage{},
		},
		{
			name: "multiple_named_and_repeated_placeholders",
			template: []ChatPromptItem{
				{Type: "placeholder", Name: "first"},
				{Type: "placeholder", Name: "second"},
				{Type: "placeholder", Name: "first"},
			},
			placeholders: map[string][]ChatMessage{
				"first":  {{Role: "user", Content: "A"}},
				"second": {{Role: "assistant", Content: "B"}},
			},
			want: []ChatMessage{{Role: "user", Content: "A"}, {Role: "assistant", Content: "B"}, {Role: "user", Content: "A"}},
		},
		{
			name:      "missing_variable",
			template:  []ChatPromptItem{{Role: "user", Content: "{{question}}"}},
			wantError: "缺少模板变量：question",
		},
		{
			name:      "extra_variables_are_sorted",
			template:  []ChatPromptItem{{Role: "user", Content: "正文"}},
			variables: map[string]string{"z": "", "a": ""},
			wantError: "存在未使用的模板变量：a, z",
		},
		{
			name:      "missing_placeholder",
			template:  []ChatPromptItem{{Type: "placeholder", Name: "history"}},
			wantError: "缺少消息占位参数：history",
		},
		{
			name:         "extra_placeholders_are_sorted",
			template:     []ChatPromptItem{{Role: "user", Content: "正文"}},
			placeholders: map[string][]ChatMessage{"z": nil, "a": nil},
			wantError:    "存在未使用的消息占位参数：a, z",
		},
		{
			name:      "blank_placeholder_name",
			template:  []ChatPromptItem{{Type: "placeholder", Name: " \t"}},
			wantError: "消息占位名称不能为空",
		},
		{
			name:      "malformed_template",
			template:  []ChatPromptItem{{Role: "user", Content: "{{question"}},
			wantError: "模板包含未闭合或不支持的变量占位符",
		},
		{
			name:      "unsupported_variable_syntax",
			template:  []ChatPromptItem{{Role: "user", Content: "{{user.name}}"}},
			wantError: "模板包含未闭合或不支持的变量占位符",
		},
		{
			name:      "unsupported_item_type",
			template:  []ChatPromptItem{{Type: "unknown", Role: "user"}},
			wantError: "消息类型不支持",
		},
		{
			name:      "unsupported_message_role",
			template:  []ChatPromptItem{{Role: "system", Content: "有效前缀"}, {Role: "tool", Content: "工具输出"}},
			wantError: "消息角色不支持",
		},
		{
			name:      "blank_message_role",
			template:  []ChatPromptItem{{Content: "正文"}},
			wantError: "消息角色不支持",
		},
		{
			name:         "unsupported_history_role",
			template:     []ChatPromptItem{{Type: "placeholder", Name: "history"}},
			placeholders: map[string][]ChatMessage{"history": {{Role: "tool", Content: "正文"}}},
			wantError:    "历史角色不支持",
		},
		{
			name:         "variables_only_mentioned_in_history_are_unused",
			template:     []ChatPromptItem{{Type: "placeholder", Name: "history"}},
			variables:    map[string]string{"question": "替换值"},
			placeholders: map[string][]ChatMessage{"history": {{Role: "user", Content: "{{question}}"}}},
			wantError:    "存在未使用的模板变量：question",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompileChat(tt.template, tt.variables, tt.placeholders)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
				}
				if got != nil {
					t.Fatalf("error returned partial messages: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("messages=%+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCompileChatDoesNotMutateInputs(t *testing.T) {
	history := make([]ChatMessage, 1, 8)
	history[0] = ChatMessage{Role: "user", Content: "历史 {{name}}"}
	template := []ChatPromptItem{
		{Type: "placeholder", Name: "history"},
		{Role: "system", Content: "你好 {{name}}"},
		{Type: "placeholder", Name: "history"},
	}
	variables := map[string]string{"name": "小王"}
	placeholders := map[string][]ChatMessage{"history": history}
	templateBefore := append([]ChatPromptItem(nil), template...)
	historyBefore := append([]ChatMessage(nil), history...)

	got, err := CompileChat(template, variables, placeholders)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(template, templateBefore) || !reflect.DeepEqual(history, historyBefore) || !reflect.DeepEqual(variables, map[string]string{"name": "小王"}) || len(placeholders) != 1 {
		t.Fatal("compilation modified inputs")
	}
	if len(got) != 3 || got[0] != historyBefore[0] || got[1].Content != "你好 小王" || got[2] != historyBefore[0] {
		t.Fatalf("wrong compiled result: %+v", got)
	}
	got[0].Content = "修改返回结果"
	got[1].Content = "修改编译正文"
	if !reflect.DeepEqual(template, templateBefore) || !reflect.DeepEqual(placeholders["history"], historyBefore) || got[2] != historyBefore[0] {
		t.Fatal("returned messages share mutable elements with inputs or repeated history")
	}
}
