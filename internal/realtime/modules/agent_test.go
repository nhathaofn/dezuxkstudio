package modules_test

import (
	"context"
	"errors"
	"testing"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/realtime"
	"app/internal/realtime/modules"
)

// In headless server mode a remote client must not widen the folders the workflow file guard trusts.
func TestAgentModule_LockedTrustedFoldersRefuseChanges(t *testing.T) {
	reg := realtime.NewRegistry(contracts.ActionNames())
	modules.Agent{LockTrustedFolders: true}.Register(reg) // Service never reached when locked

	for _, action := range []string{contracts.ActionAgentAddAllowedDir, contracts.ActionAgentRemoveAllowedDir} {
		h, ok := reg.Lookup(action)
		if !ok {
			t.Fatalf("%s not registered", action)
		}
		_, err := h(context.Background(), &realtime.Request{}, []byte(`{"dir":"C:/Users/someone/Documents"}`))
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeUnavailable {
			t.Fatalf("%s: expected ERR_UNAVAILABLE, got %v", action, err)
		}
	}
}
