package buildinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// wails.json drives packaging (window resources, installer, bundle name) and must
// agree with the runtime identity compiled into the binary.
func TestWailsJSONMatchesBuildInfo(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "wails.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name           string `json:"name"`
		OutputFilename string `json:"outputfilename"`
		Info           struct {
			ProductName string `json:"productName"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for field, v := range map[string]string{
		"name": doc.Name, "outputfilename": doc.OutputFilename, "info.productName": doc.Info.ProductName,
	} {
		if v != ProductName {
			t.Errorf("wails.json %s = %q, buildinfo.ProductName = %q", field, v, ProductName)
		}
	}
}
