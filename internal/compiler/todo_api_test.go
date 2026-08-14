package compiler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"slick/internal/compiler"
)

type todoJSON struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type errorJSON struct {
	Error string `json:"error"`
}

type healthJSON struct {
	Status string `json:"status"`
}


func startTodoAPINative(t *testing.T, binary, address, dbPath string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	command := exec.Command(binary)
	command.Env = append(os.Environ(),
		"TODO_API_ADDRESS="+address,
		"TODO_API_DATABASE="+dbPath,
	)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start native todo API: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	return command, &output
}

func startTodoAPIInterpreter(t *testing.T, projectPath, address, dbPath string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	slick := buildSlickTool(t)
	command := exec.Command(slick, "run", projectPath)
	command.Env = append(os.Environ(),
		"TODO_API_ADDRESS="+address,
		"TODO_API_DATABASE="+dbPath,
	)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start interpreter todo API: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_, _ = command.Process.Wait()
		}
	})
	return command, &output
}

func waitForHealth(t *testing.T, baseURL string) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/health")
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && strings.Contains(string(body), `"status":"ok"`) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("todo API on %s did not become healthy within deadline", baseURL)
}

func stopGracefully(t *testing.T, command *exec.Cmd, output *strings.Builder) {
	t.Helper()
	if command.Process == nil {
		return
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := command.Process.Wait()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exit after SIGTERM: %v\noutput:\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("server did not exit within deadline after SIGTERM\noutput:\n%s", output.String())
	}
}

func runTodoAPITestSuite(t *testing.T, baseURL string) {
	t.Helper()

	// 1. Health endpoint
	{
		resp, err := http.Get(baseURL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /health status=%d, want 200; body=%q", resp.StatusCode, body)
		}
		if contentType := resp.Header.Get("Content-Type"); contentType != "application/json" {
			t.Fatalf("GET /health Content-Type=%q, want application/json", contentType)
		}
		var health healthJSON
		if err := json.Unmarshal(body, &health); err != nil || health.Status != "ok" {
			t.Fatalf("GET /health invalid JSON body: %q", body)
		}
	}

	// 2. Initial List: empty
	{
		resp, err := http.Get(baseURL + "/todos")
		if err != nil {
			t.Fatalf("GET /todos: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /todos status=%d, want 200; body=%q", resp.StatusCode, body)
		}
		if contentType := resp.Header.Get("Content-Type"); contentType != "application/json" {
			t.Fatalf("GET /todos Content-Type=%q, want application/json", contentType)
		}
		var list []todoJSON
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("GET /todos unmarshal: %v; body=%q", err, body)
		}
		if len(list) != 0 {
			t.Fatalf("GET /todos initial len=%d, want 0", len(list))
		}
	}

	// 3. Create a todo
	var createdID int
	{
		reqBody := `{"title":"write API"}`
		resp, err := http.Post(baseURL+"/todos", "application/json", strings.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST /todos: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /todos status=%d, want 201; body=%q", resp.StatusCode, body)
		}
		if contentType := resp.Header.Get("Content-Type"); contentType != "application/json" {
			t.Fatalf("POST /todos Content-Type=%q, want application/json", contentType)
		}
		var created todoJSON
		if err := json.Unmarshal(body, &created); err != nil {
			t.Fatalf("POST /todos unmarshal: %v; body=%q", err, body)
		}
		if created.ID <= 0 || created.Title != "write API" || created.Completed != false {
			t.Fatalf("POST /todos created unexpected object: %+v", created)
		}
		createdID = created.ID
	}

	// 4. Create with SQL injection and special characters in title
	const sqlInjectionTitle = `test ' " -- DROP TABLE todos; \n \t`
	var sqlTodoID int
	{
		reqPayload, _ := json.Marshal(map[string]string{"title": sqlInjectionTitle})
		resp, err := http.Post(baseURL+"/todos", "application/json", bytes.NewReader(reqPayload))
		if err != nil {
			t.Fatalf("POST /todos special chars: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /todos special chars status=%d, want 201; body=%q", resp.StatusCode, body)
		}
		var created todoJSON
		if err := json.Unmarshal(body, &created); err != nil {
			t.Fatalf("POST /todos special chars unmarshal: %v", err)
		}
		if created.Title != sqlInjectionTitle {
			t.Fatalf("POST /todos special chars title=%q, want %q", created.Title, sqlInjectionTitle)
		}
		sqlTodoID = created.ID
	}

	// 5. Read todo by ID
	{
		resp, err := http.Get(baseURL + "/todos/" + strconv.Itoa(createdID))
		if err != nil {
			t.Fatalf("GET /todos/%d: %v", createdID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /todos/%d status=%d, want 200; body=%q", createdID, resp.StatusCode, body)
		}
		var todo todoJSON
		if err := json.Unmarshal(body, &todo); err != nil {
			t.Fatalf("GET /todos/%d unmarshal: %v", createdID, err)
		}
		if todo.ID != createdID || todo.Title != "write API" || todo.Completed != false {
			t.Fatalf("GET /todos/%d unexpected object: %+v", createdID, todo)
		}
	}

	// 6. Read non-existent todo
	{
		resp, err := http.Get(baseURL + "/todos/99999")
		if err != nil {
			t.Fatalf("GET /todos/99999: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /todos/99999 status=%d, want 404; body=%q", resp.StatusCode, body)
		}
		var errResp errorJSON
		if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error != "todo not found" {
			t.Fatalf("GET /todos/99999 body=%q, want error 'todo not found'", body)
		}
	}

	// 7. Invalid todo IDs: non-integer, negative, zero
	for _, invalidID := range []string{"abc", "-1", "0", "1.5"} {
		resp, err := http.Get(baseURL + "/todos/" + invalidID)
		if err != nil {
			t.Fatalf("GET /todos/%s: %v", invalidID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET /todos/%s status=%d, want 400; body=%q", invalidID, resp.StatusCode, body)
		}
		var errResp errorJSON
		if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error != "invalid todo id" {
			t.Fatalf("GET /todos/%s body=%q, want error 'invalid todo id'", invalidID, body)
		}
	}

	// 8. Update todo
	{
		reqBody := `{"title":"ship API","completed":true}`
		req, _ := http.NewRequest(http.MethodPut, baseURL+"/todos/"+strconv.Itoa(createdID), strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT /todos/%d: %v", createdID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /todos/%d status=%d, want 200; body=%q", createdID, resp.StatusCode, body)
		}
		var updated todoJSON
		if err := json.Unmarshal(body, &updated); err != nil {
			t.Fatalf("PUT /todos/%d unmarshal: %v", createdID, err)
		}
		if updated.ID != createdID || updated.Title != "ship API" || updated.Completed != true {
			t.Fatalf("PUT /todos/%d unexpected object: %+v", createdID, updated)
		}
	}

	// 9. Verify updated state through GET
	{
		resp, err := http.Get(baseURL + "/todos/" + strconv.Itoa(createdID))
		if err != nil {
			t.Fatalf("GET /todos/%d after update: %v", createdID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var todo todoJSON
		_ = json.Unmarshal(body, &todo)
		if todo.Title != "ship API" || todo.Completed != true {
			t.Fatalf("GET /todos/%d after update: %+v", createdID, todo)
		}
	}

	// 10. Update non-existent todo
	{
		reqBody := `{"title":"ghost","completed":true}`
		req, _ := http.NewRequest(http.MethodPut, baseURL+"/todos/99999", strings.NewReader(reqBody))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT /todos/99999: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("PUT /todos/99999 status=%d, want 404; body=%q", resp.StatusCode, body)
		}
	}

	// 11. Validation errors: empty/whitespace-only titles (422)
	for _, emptyTitle := range []string{`{"title":""}`, `{"title":"   \t  "}`} {
		resp, err := http.Post(baseURL+"/todos", "application/json", strings.NewReader(emptyTitle))
		if err != nil {
			t.Fatalf("POST empty title: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("POST empty title status=%d, want 422; body=%q", resp.StatusCode, body)
		}
	}
	{
		req, _ := http.NewRequest(http.MethodPut, baseURL+"/todos/"+strconv.Itoa(createdID), strings.NewReader(`{"title":"   ","completed":false}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT empty title: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("PUT empty title status=%d, want 422; body=%q", resp.StatusCode, body)
		}
	}

	// 12. Malformed JSON (400)
	{
		resp, err := http.Post(baseURL+"/todos", "application/json", strings.NewReader(`{invalid json`))
		if err != nil {
			t.Fatalf("POST malformed json: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST malformed json status=%d, want 400; body=%q", resp.StatusCode, body)
		}
	}

	// 13. Method Not Allowed (405) with Allow headers
	{
		resp, err := http.Post(baseURL+"/health", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("POST /health: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("POST /health status=%d, want 405", resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != "GET" {
			t.Fatalf("POST /health Allow header=%q, want 'GET'", allow)
		}
	}
	{
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/todos", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE /todos: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("DELETE /todos status=%d, want 405", resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != "GET, POST" {
			t.Fatalf("DELETE /todos Allow header=%q, want 'GET, POST'", allow)
		}
	}
	{
		req, _ := http.NewRequest(http.MethodPatch, baseURL+"/todos/1", strings.NewReader(`{}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH /todos/1: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("PATCH /todos/1 status=%d, want 405", resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != "GET, PUT, DELETE" {
			t.Fatalf("PATCH /todos/1 Allow header=%q, want 'GET, PUT, DELETE'", allow)
		}
	}

	// 14. Unknown Path (404)
	{
		resp, err := http.Get(baseURL + "/nonexistent-route")
		if err != nil {
			t.Fatalf("GET /nonexistent-route: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /nonexistent-route status=%d, want 404; body=%q", resp.StatusCode, body)
		}
	}
	{
		resp, err := http.Get(baseURL + "/todos/1/subpath")
		if err != nil {
			t.Fatalf("GET /todos/1/subpath: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /todos/1/subpath status=%d, want 404; body=%q", resp.StatusCode, body)
		}
	}

	// 15. Concurrent Creates
	const concurrentCount = 10
	var wg sync.WaitGroup
	concurrentIDs := make([]int, concurrentCount)
	for i := range concurrentCount {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			title := fmt.Sprintf("concurrent task %d", index)
			payload, _ := json.Marshal(map[string]string{"title": title})
			resp, err := http.Post(baseURL+"/todos", "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Errorf("concurrent POST %d: %v", index, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				t.Errorf("concurrent POST %d status=%d; body=%q", index, resp.StatusCode, body)
				return
			}
			var item todoJSON
			if err := json.Unmarshal(body, &item); err != nil {
				t.Errorf("concurrent POST %d unmarshal: %v", index, err)
				return
			}
			concurrentIDs[index] = item.ID
		}(i)
	}
	wg.Wait()

	// Verify all concurrent IDs are unique and positive
	seenIDs := make(map[int]bool)
	for _, id := range concurrentIDs {
		if id <= 0 {
			t.Fatalf("concurrent create produced invalid id %d", id)
		}
		if seenIDs[id] {
			t.Fatalf("concurrent create produced duplicate id %d", id)
		}
		seenIDs[id] = true
	}

	// 16. Delete todo
	{
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/todos/"+strconv.Itoa(createdID), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE /todos/%d: %v", createdID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE /todos/%d status=%d, want 204; body=%q", createdID, resp.StatusCode, body)
		}
		if len(body) != 0 {
			t.Fatalf("DELETE /todos/%d body len=%d, want 0", createdID, len(body))
		}
	}

	// 17. Delete already-deleted todo (404)
	{
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/todos/"+strconv.Itoa(createdID), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE /todos/%d second time: %v", createdID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("DELETE /todos/%d second time status=%d, want 404; body=%q", createdID, resp.StatusCode, body)
		}
	}

	// 18. Read deleted todo (404)
	{
		resp, err := http.Get(baseURL + "/todos/" + strconv.Itoa(createdID))
		if err != nil {
			t.Fatalf("GET /todos/%d after delete: %v", createdID, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /todos/%d after delete status=%d, want 404; body=%q", createdID, resp.StatusCode, body)
		}
	}

	// 19. Verify list includes SQL injection todo and concurrent todos
	{
		resp, err := http.Get(baseURL + "/todos")
		if err != nil {
			t.Fatalf("GET /todos final: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var list []todoJSON
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("GET /todos final unmarshal: %v", err)
		}
		// Expect 1 (sql injection) + concurrentCount (10) = 11 items
		if len(list) != 1+concurrentCount {
			t.Fatalf("GET /todos final count=%d, want %d", len(list), 1+concurrentCount)
		}
		// Verify list is ordered by ascending ID
		for i := 1; i < len(list); i++ {
			if list[i].ID <= list[i-1].ID {
				t.Fatalf("GET /todos list not sorted: [%d].ID=%d <= [%d].ID=%d", i, list[i].ID, i-1, list[i-1].ID)
			}
		}
		// Verify SQL injection todo is in list
		foundSQL := false
		for _, item := range list {
			if item.ID == sqlTodoID && item.Title == sqlInjectionTitle {
				foundSQL = true
				break
			}
		}
		if !foundSQL {
			t.Fatalf("GET /todos did not find sql injection todo %d with title %q", sqlTodoID, sqlInjectionTitle)
		}
	}
}

func TestTodoAPINativeEndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	binary := filepath.Join(tempDir, "todo-api-bin")
	diagnostics, err := compiler.BuildPath(filepath.Join("..", "..", "examples", "todo-api"), binary)
	if err != nil {
		t.Fatalf("build todo-api: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)

	dbPath := filepath.Join(tempDir, "todos.db")
	address := freeLoopbackAddress(t)
	command, output := startTodoAPINative(t, binary, address, dbPath)
	baseURL := "http://" + address
	waitForHealth(t, baseURL)

	runTodoAPITestSuite(t, baseURL)

	stopGracefully(t, command, output)

	// Test persistence across process restart
	address2 := freeLoopbackAddress(t)
	command2, output2 := startTodoAPINative(t, binary, address2, dbPath)
	baseURL2 := "http://" + address2
	waitForHealth(t, baseURL2)

	// Verify committed data persisted
	resp, err := http.Get(baseURL2 + "/todos")
	if err != nil {
		t.Fatalf("GET /todos after restart: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var list []todoJSON
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("GET /todos after restart unmarshal: %v", err)
	}
	if len(list) != 11 {
		t.Fatalf("GET /todos after restart count=%d, want 11", len(list))
	}

	stopGracefully(t, command2, output2)
}

func TestTodoAPIInterpreterEndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "todos-interp.db")
	projectPath := filepath.Join("..", "..", "examples", "todo-api")
	address := freeLoopbackAddress(t)
	command, output := startTodoAPIInterpreter(t, projectPath, address, dbPath)
	baseURL := "http://" + address
	waitForHealth(t, baseURL)

	runTodoAPITestSuite(t, baseURL)

	stopGracefully(t, command, output)

	// Test persistence across interpreter restart
	address2 := freeLoopbackAddress(t)
	command2, output2 := startTodoAPIInterpreter(t, projectPath, address2, dbPath)
	baseURL2 := "http://" + address2
	waitForHealth(t, baseURL2)

	resp, err := http.Get(baseURL2 + "/todos")
	if err != nil {
		t.Fatalf("GET /todos after interpreter restart: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var list []todoJSON
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("GET /todos after interpreter restart unmarshal: %v", err)
	}
	if len(list) != 11 {
		t.Fatalf("GET /todos after interpreter restart count=%d, want 11", len(list))
	}

	stopGracefully(t, command2, output2)
}

func TestTodoAPIFormatFixedPoint(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "todo-api", "*.slk"))
	if err != nil {
		t.Fatalf("glob todo-api: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no .slk files found in examples/todo-api")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source := compiler.Source{
			Name:      filepath.Base(file),
			Namespace: "root",
			Text:      string(data),
		}
		formatted, diagnostics, err := compiler.Format(source)
		if err != nil || len(diagnostics) != 0 {
			t.Fatalf("format %s: diagnostics=%+v err=%v", file, diagnostics, err)
		}
		if formatted != string(data) {
			t.Fatalf("file %s is not a format fixed point:\n--- Got ---\n%s\n--- Want ---\n%s", file, formatted, string(data))
		}
	}
}

func TestTodoAPIConfigFailure(t *testing.T) {
	tempDir := t.TempDir()
	binary := filepath.Join(tempDir, "todo-api-bin")
	diagnostics, err := compiler.BuildPath(filepath.Join("..", "..", "examples", "todo-api"), binary)
	if err != nil {
		t.Fatalf("build todo-api: %v", err)
	}
	assertNoDiagnostics(t, diagnostics)

	// Run without TODO_API_DATABASE
	command := exec.Command(binary)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure when TODO_API_DATABASE is unset, but command succeeded")
	}
	if !strings.Contains(string(output), "TODO_API_DATABASE environment variable is required") {
		t.Fatalf("output %q does not mention required TODO_API_DATABASE", string(output))
	}
}
