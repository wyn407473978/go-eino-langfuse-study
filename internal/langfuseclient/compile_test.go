package langfuseclient

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompileText(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]string
		want      string
		wantError string
	}{
		{
			name: "替换多个变量", template: "你是 {{service_name}}，欢迎 {{user}}。",
			variables: map[string]string{"service_name": "政务服务中心", "user": "小王"},
			want:      "你是 政务服务中心，欢迎 小王。",
		},
		{
			name: "重复变量及括号内空白", template: "{{name}}，欢迎 {{ \tname\n }}！",
			variables: map[string]string{"name": "小王"}, want: "小王，欢迎 小王！",
		},
		{
			name: "允许空字符串值", template: "你好{{name}}！",
			variables: map[string]string{"name": ""}, want: "你好！",
		},
		{name: "无变量模板允许nil参数", template: "你好", want: "你好"},
		{name: "空模板原样返回", template: "", want: ""},
		{name: "空白模板原样返回", template: " \n\t", want: " \n\t"},
		{
			name: "JSON单大括号保留", template: `{"name":"{{name}}"}`,
			variables: map[string]string{"name": "小王"}, want: `{"name":"小王"}`,
		},
		{
			name: "插入值保持字面且不递归", template: "{{name}} / {{name}}",
			variables: map[string]string{"name": `$1\path {{other}}`},
			want:      `$1\path {{other}} / $1\path {{other}}`,
		},
		{
			name: "nil参数缺少变量", template: "你好{{name}}", wantError: "缺少模板变量：name",
		},
		{
			name: "空map缺少变量", template: "{{name}}",
			variables: map[string]string{}, wantError: "缺少模板变量：name",
		},
		{
			name: "部分变量缺失", template: "{{name}} {{role}}",
			variables: map[string]string{"name": "小王"}, wantError: "缺少模板变量：role",
		},
		{
			name: "拒绝额外变量", template: "{{name}}",
			variables: map[string]string{"name": "小王", "age": "18"},
			wantError: "存在未使用的模板变量：age",
		},
		{
			name: "无占位符时拒绝额外变量", template: "你好",
			variables: map[string]string{"name": "小王"},
			wantError: "存在未使用的模板变量：name",
		},
		{
			name: "额外变量排序", template: "{{name}}",
			variables: map[string]string{"name": "小王", "z": "", "a": ""},
			wantError: "存在未使用的模板变量：a, z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 准备：保存输入，检查编译不会修改调用方的 map。
			var before map[string]string
			if tt.variables != nil {
				before = make(map[string]string, len(tt.variables))
				for key, value := range tt.variables {
					before[key] = value
				}
			}

			// 执行：测试只使用本地模板，不访问 Langfuse 或模型。
			got, err := CompileText(tt.template, tt.variables)

			// 断言：错误时不返回半成品，成功时输出应完全一致。
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("CompileText() error = %v, want containing %q", err, tt.wantError)
				}
				if got != "" {
					t.Errorf("失败时返回了部分编译结果：%q", got)
				}
			} else {
				if err != nil {
					t.Fatalf("CompileText() error = %v", err)
				}
				if got != tt.want {
					t.Errorf("CompileText() = %q, want %q", got, tt.want)
				}
			}
			if !reflect.DeepEqual(tt.variables, before) {
				t.Errorf("CompileText() 修改了输入变量：got %v, want %v", tt.variables, before)
			}
		})
	}
}

func TestCompileTextRejectsInvalidPlaceholders(t *testing.T) {
	for _, template := range []string{
		"{{}}", "{{ }}", "{{name", "name}}", "{{first name}}",
		"{{1name}}", "{{service-name}}", "{{用户}}", "{{name}} {{broken",
	} {
		t.Run(template, func(t *testing.T) {
			got, err := CompileText(template, map[string]string{"name": "小王"})
			if err == nil || !strings.Contains(err.Error(), "占位符") {
				t.Fatalf("CompileText() error = %v, want invalid placeholder error", err)
			}
			if got != "" {
				t.Errorf("非法模板返回了部分结果：%q", got)
			}
		})
	}
}
