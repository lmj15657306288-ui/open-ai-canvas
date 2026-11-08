package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentModelSelectionRejectsBeforeCanvasRead(t *testing.T) {
	for _, tc := range []struct{ name, args, field, issue string }{
		{"missing", `{}`, "logicalModelId", "required"},
		{"empty", `{"logicalModelId":"","channelId":"","channelModelKey":""}`, "logicalModelId", "required"},
		{"missing channel", `{"channelModelKey":"image"}`, "channelId", "required"},
		{"missing key", `{"channelId":"channel"}`, "channelModelKey", "required"},
		{"mixed", `{"logicalModelId":"model","channelId":"channel","channelModelKey":"image"}`, "logicalModelId", "mutually_exclusive"},
		{"partial mixed", `{"logicalModelId":"model","channelModelKey":"image"}`, "logicalModelId", "mutually_exclusive"},
		{"whitespace", `{"channelId":" \t","channelModelKey":"image"}`, "channelId", "invalid_value"},
		{"null", `{"logicalModelId":null,"channelId":"channel","channelModelKey":"image"}`, "logicalModelId", "type_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := cloudAgentCall{ID: "invalid"}
			call.Function.Name, call.Function.Arguments = "generate_media", tc.args
			// No service/repository: rejection must precede any read or side effect.
			var s *Service
			_, _, err := s.prepareCloudAgentMedia(nil, nil, call)
			var fieldErr *cloudAgentFieldArgumentError
			if !errors.As(err, &fieldErr) || fieldErr.Field != tc.field || fieldErr.Issue != tc.issue {
				t.Fatalf("unexpected validation: %#v", err)
			}
			req := agentTestRequest()
			req.PermissionMode = "auto"
			state := &cloudAgentRuntime{Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
			cloudAgentToolResult("run", state, call, map[string]any{"phase": "admission", "taskSubmitted": false}, err)
			var result map[string]any
			if err := json.Unmarshal([]byte(state.Canonical.Messages[0]["content"].(string)), &result); err != nil {
				t.Fatal(err)
			}
			if result["field"] != tc.field || result["issue"] != tc.issue || result["reason"] != "invalid_tool_arguments" || result["parameters"] == nil || result["taskSubmitted"] != false {
				t.Fatalf("missing strict repair feedback: %#v", result)
			}
		})
	}
}

func TestCloudAgentModelSelectionAcceptsOnlyCompleteSelections(t *testing.T) {
	for _, raw := range []string{
		`{"logicalModelId":"model"}`,
		`{"logicalModelId":"model","channelId":"","channelModelKey":""}`,
		`{"channelId":"channel","channelModelKey":"image"}`,
		`{"logicalModelId":"","channelId":"channel","channelModelKey":"image"}`,
	} {
		var args cloudAgentMediaArgs
		if err := decodeCloudAgentJSONObject(raw, &args); err != nil {
			t.Fatal(err)
		}
		if err := validateCloudAgentModelSelection(raw, args); err != nil {
			t.Fatalf("valid selection rejected: %v", err)
		}
	}
}

func TestCloudAgentMediaMalformedJSONKeepsStrictFeedback(t *testing.T) {
	for _, raw := range []string{`{"unknown":"private-sentinel"}`, `{"channelId":3}`, `{} {}`, `null`} {
		call := cloudAgentCall{}
		call.Function.Arguments = raw
		var s *Service
		_, _, err := s.prepareCloudAgentMedia(nil, nil, call)
		var argumentErr *cloudAgentArgumentError
		if !errors.As(err, &argumentErr) || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatalf("malformed arguments lost safe schema error: %v", err)
		}
	}
}

func TestCloudAgentMediaToolsDeclareExclusiveModelSelection(t *testing.T) {
	count := 0
	req := agentTestRequest()
	req.PermissionMode = "auto"
	for _, tool := range cloudAgentTools(req) {
		function := tool["function"].(map[string]any)
		if function["name"] != "generate_media" && function["name"] != "image_layer_split" {
			continue
		}
		count++
		parameters := function["parameters"].(map[string]any)
		if !strings.Contains(function["description"].(string), cloudAgentModelSelectionDescription) || parameters["additionalProperties"] != false {
			t.Fatalf("incomplete model selection schema: %#v", parameters)
		}
		// OpenAI 兼容网关会静默丢弃带顶层组合关键字的工具（生产实测：三个媒体工具
		// 因此在模型工具清单里整批消失），互斥只能写在字段描述里、由服务端强制。
		for _, combinator := range []string{"oneOf", "anyOf", "allOf"} {
			if _, ok := parameters[combinator]; ok {
				t.Fatalf("top-level %s drops the tool on OpenAI-compatible gateways: %#v", combinator, parameters[combinator])
			}
		}
		props := parameters["properties"].(map[string]any)
		for _, field := range []string{"logicalModelId", "channelId", "channelModelKey"} {
			property := props[field].(map[string]any)
			if property["type"] != "string" {
				t.Fatalf("model field contract drift: %s %#v", field, property)
			}
			if description, _ := property["description"].(string); !strings.Contains(description, "二选一") {
				t.Fatalf("model field must state the exclusive rule in text: %s %#v", field, property)
			}
		}
	}
	if count != 2 {
		t.Fatalf("missing media tools: %d", count)
	}
}

func TestCloudAgentMissingModelCreatesNoDraftApprovalOrTask(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	args.ChannelID, args.ChannelModelKey = "", ""
	run, _ := agentMediaRun(t, s, args, "auto")
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	var before int64
	if err := db.Model(&model.Task{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	var billingBefore int64
	if err := db.Model(&model.BillingOrder{}).Count(&billingBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	afterCanvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	var after int64
	if err := db.Model(&model.Task{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	var billingAfter int64
	if err := db.Model(&model.BillingOrder{}).Count(&billingAfter).Error; err != nil {
		t.Fatal(err)
	}
	if before != after || billingBefore != billingAfter || canvas.PayloadJSON != afterCanvas.PayloadJSON || state.Approval != nil || state.MediaTaskID != "" {
		t.Fatal("invalid selection created a task, draft, approval or billing order")
	}
	found := false
	for _, event := range state.Events {
		if event.Type == "tool_failed" {
			result, _ := event.Payload["result"].(map[string]any)
			found = result["reason"] == "invalid_tool_arguments" && result["taskSubmitted"] == false
		}
	}
	if !found {
		t.Fatal("admission failure was silently hidden")
	}
}

// TestCloudAgentToolSchemasStayGatewayCompatible 守住一次真实事故：OpenAI 兼容网关
// （生产是 Magpie）会静默丢弃 parameters 顶层带 oneOf/anyOf/allOf 的工具。上一版把
// 模型选择互斥写成顶层 oneOf，三个媒体工具因此在模型工具清单里整批消失，Agent 只能
// 回复「系统没有给我提交生图任务的工具」，用户侧表现为「没有生成权限」。
func TestCloudAgentToolSchemasStayGatewayCompatible(t *testing.T) {
	req := agentTestRequest()
	req.PermissionMode = "auto"
	tools := cloudAgentTools(req)
	if len(tools) == 0 {
		t.Fatal("no tools compiled")
	}
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		parameters, _ := function["parameters"].(map[string]any)
		if parameters == nil {
			t.Fatalf("%s has no parameter schema", name)
		}
		// 组合关键字只允许出现在嵌套节点（如 canvas_apply_ops 的 ops.items），顶层一律禁止。
		for _, combinator := range []string{"oneOf", "anyOf", "allOf"} {
			if _, ok := parameters[combinator]; ok {
				t.Fatalf("%s declares top-level %s; OpenAI-compatible gateways drop the whole tool", name, combinator)
			}
		}
	}
}
