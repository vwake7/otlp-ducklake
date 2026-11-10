package config

import "testing"

func TestLoadRequiresSecrets(t *testing.T) {
	t.Setenv("QUACK_TOKEN", "")
	t.Setenv("S3_ACCESS_KEY_ID", "")
	t.Setenv("S3_SECRET_ACCESS_KEY", "")
	t.Setenv("S3_BUCKET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateShared(); err == nil {
		t.Fatal("expected missing settings")
	}
}

func TestDataPathAndQuackURI(t *testing.T) {
	cfg := Config{QuackHost: "quack", QuackPort: 9494, S3Bucket: "logs", S3Prefix: "lake"}
	if got := cfg.QuackURI(); got != "quack:quack:9494" {
		t.Fatalf("uri %s", got)
	}
	if got := cfg.DataPath(); got != "s3://logs/lake" {
		t.Fatalf("path %s", got)
	}
}

func TestStripEndpoint(t *testing.T) {
	if got := stripEndpoint("https://s3.gra.io.cloud.ovh.net/"); got != "s3.gra.io.cloud.ovh.net" {
		t.Fatalf("endpoint %s", got)
	}
}
