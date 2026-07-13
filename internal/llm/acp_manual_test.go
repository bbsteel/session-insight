package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestManualACPListModels(t *testing.T) {
	if os.Getenv("SI_ACP_MANUAL") == "" {
		t.Skip("manual test")
	}
	client, err := New(Config{Kind: "acp", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	models, err := client.ListModels(ctx)
	t.Logf("elapsed=%s err=%v models=%d", time.Since(start), err, len(models))
	for i, m := range models {
		if i < 5 {
			t.Logf("  - %s %s", m.ID, m.Label)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}
