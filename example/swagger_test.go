package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSwaggerDocs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping swag regeneration in -short mode")
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "github.com/swaggo/swag/cmd/swag@v1.16.6",
		"init", "--parseDependency", "--output", filepath.Join(dir, "docs"))
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("swag init: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "docs", "swagger.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Tags        []string `json:"tags"`
			OperationID string   `json:"operationId"`
		} `json:"paths"`
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		"/user.v1.UserService/GetUser",
		"/user.v1.UserService/UpdateUser",
		"/user.v1.NotificationService/Send",
	} {
		if _, ok := spec.Paths[p]; !ok {
			t.Errorf("swagger paths missing %s; got %v", p, spec.Paths)
		}
	}
	if _, ok := spec.Paths["/user.v1.UserService/WatchUsers"]; ok {
		t.Error("streaming method must not appear in swagger paths")
	}
	if op := spec.Paths["/user.v1.UserService/GetUser"]["post"]; op.OperationID != "user.v1.UserService.GetUser" {
		t.Errorf("GetUser operationId = %q", op.OperationID)
	}
	if _, ok := spec.Definitions["ginruntime.Error"]; !ok {
		t.Errorf("definitions missing ginruntime.Error; got %v", spec.Definitions)
	}
}
