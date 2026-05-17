package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"IpacPanel/controller/src/atomic/file"
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/web"
)

func TestHandleApiControllerUpdateUploadInitRejectsNonZipFileName(t *testing.T) {
	cleanup := setupControllerUpdateAPITest(t)
	defer cleanup()

	recorder := httptest.NewRecorder()
	request := newControllerUpdateInitTestRequest(t, map[string]any{
		"name":        "IpacPanel_Controller.exe",
		"size":        1,
		"chunk_size":  1,
		"chunk_count": 1,
	})

	HandleApiControllerUpdateUploadInit(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("非 .zip 更新文件名应被拒绝, got status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeControllerUpdateTestResponse(t, recorder)
	if !strings.Contains(strings.ToLower(response.Message), ".zip") {
		t.Fatalf("非 .zip 更新文件名应返回明确的 .zip 校验错误, got message=%q body=%s", response.Message, recorder.Body.String())
	}
}

func TestHandleApiControllerUpdateUploadInitAcceptsZipFileName(t *testing.T) {
	cleanup := setupControllerUpdateAPITest(t)
	defer cleanup()

	recorder := httptest.NewRecorder()
	request := newControllerUpdateInitTestRequest(t, map[string]any{
		"name":        "IpacPanel-windows-amd64.zip",
		"size":        1,
		"chunk_size":  1,
		"chunk_count": 1,
	})

	HandleApiControllerUpdateUploadInit(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf(".zip 更新文件名应允许上传初始化, got status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPrepareControllerUpdateBinaryMustExtractFromZipBeforeVersionParse(t *testing.T) {
	source := readControllerUpdateTestFile(t, "controller_update.go")
	body := extractControllerUpdateTestFunctionBody(t, source, "func prepareControllerUpdateBinary")
	directParseIndex := strings.Index(body, "parseControllerVersion(uploadPath)")
	if directParseIndex < 0 {
		return
	}
	extractIndex := strings.Index(body, "extractControllerFromZip(uploadPath")
	if extractIndex < 0 {
		t.Fatalf("prepareControllerUpdateBinary 应从 zip 提取管理进程二进制")
	}
	if directParseIndex < extractIndex {
		t.Fatalf("prepareControllerUpdateBinary 不应直接解析上传文件, 必须先从 zip 提取")
	}
}

func TestPanelSettingsControllerUpdateUploadOnlyAdvertisesZip(t *testing.T) {
	source := readControllerUpdateTestFile(t, filepath.Join("..", "..", "..", "public", "src", "module", "panelSettingsModal.js"))
	if strings.Contains(source, "IpacPanel_Controller[.exe]") {
		t.Fatalf("管理进程更新上传说明不应再包含 IpacPanel_Controller[.exe]")
	}
	if !strings.Contains(source, `id="controllerUpdateFileInput"`) {
		t.Fatalf("未找到管理进程更新文件输入框")
	}
	if !strings.Contains(source, `accept=".zip"`) {
		t.Fatalf("管理进程更新文件输入框 accept 应只允许 .zip")
	}
}

func setupControllerUpdateAPITest(t *testing.T) func() {
	t.Helper()
	previousConfig := cfg.CurrentConfig
	tempDir := t.TempDir()
	registryPath := filepath.Join(tempDir, "temp.yml")
	file.SetRegistryPath(registryPath)
	cfg.ManagerMu.Lock()
	cfg.CurrentConfig.Auth = []cfg.AuthUser{{User: "admin", Perm: 7}}
	cfg.ManagerMu.Unlock()
	resetUploadSessions()
	return func() {
		resetUploadSessions()
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = previousConfig
		cfg.ManagerMu.Unlock()
	}
}

func newControllerUpdateInitTestRequest(t *testing.T, payload map[string]any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("编码测试请求失败: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/controller/update/upload/init", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "controller-update-test-csrf")
	request.AddCookie(&http.Cookie{Name: "csrf", Value: "controller-update-test-csrf"})
	token, err := web.GetOrCreateUserToken("admin")
	if err != nil {
		t.Fatalf("创建测试用户 token 失败: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: "auth", Value: token})
	return request
}

func decodeControllerUpdateTestResponse(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
} {
	t.Helper()
	var response struct {
		OK      bool            `json:"ok"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return response
}

func readControllerUpdateTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取测试文件 %s 失败: %v", path, err)
	}
	return string(data)
}

func extractControllerUpdateTestFunctionBody(t *testing.T, source string, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start < 0 {
		t.Fatalf("未找到函数 %s", signature)
	}
	open := strings.Index(source[start:], "{")
	if open < 0 {
		t.Fatalf("函数 %s 缺少函数体", signature)
	}
	bodyStart := start + open
	depth := 0
	for index := bodyStart; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[bodyStart : index+1]
			}
		}
	}
	t.Fatalf("函数 %s 函数体不完整", signature)
	return ""
}
