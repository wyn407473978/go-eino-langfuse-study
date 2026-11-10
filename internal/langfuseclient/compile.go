package langfuseclient

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CompileText 校验并替换简单文本变量，不修改原模板或 variables。
// 缺少变量、提供额外变量或占位符格式无效时返回错误。
func CompileText(template string, variables map[string]string) (string, error) {
	pattern := regexp.MustCompile(
		`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`,
	)
	matches := pattern.FindAllStringSubmatch(template, -1)
	// 先移除合法占位符；残留的双大括号代表未闭合或不支持的语法。
	remaining := pattern.ReplaceAllString(template, "")
	if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
		return "", fmt.Errorf("模板包含未闭合或不支持的变量占位符")
	}

	used := make(map[string]bool, len(matches))
	for _, match := range matches {
		// match[0] 是完整占位符，match[1] 是变量名。
		name := match[1]
		// nil map 也可以安全读取；ok 区分缺失变量和空字符串值。
		if _, ok := variables[name]; !ok {
			return "", fmt.Errorf("缺少模板变量：%s", name)
		}
		used[name] = true
	}

	var extraNames []string
	for name := range variables {
		if !used[name] {
			extraNames = append(extraNames, name)
		}
	}
	if len(extraNames) > 0 {
		// map 遍历顺序不固定，排序让错误提示保持稳定。
		sort.Strings(extraNames)
		return "", fmt.Errorf("存在未使用的模板变量：%s", strings.Join(extraNames, ", "))
	}

	// 只替换原模板中的占位符，插入值里的 $、反斜杠和 {{...}} 都保持原样。
	compiled := pattern.ReplaceAllStringFunc(template, func(match string) string {
		name := pattern.FindStringSubmatch(match)[1]
		return variables[name]
	})
	return compiled, nil
}
