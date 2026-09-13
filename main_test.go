package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestAdminConfigDisabledWithoutPassword(t *testing.T) {
	t.Setenv(adminPasswordEnvironment, "")
	t.Setenv(adminTrustProxyHTTPSEnvironment, "")
	t.Setenv(adminTrustedProxyCIDRsEnvironment, "")
	t.Setenv("CONTENT_DIR", "")

	config, err := loadAdminConfig()
	if err != nil {
		t.Fatalf("loadAdminConfig returned an error: %v", err)
	}
	if config.Enabled() {
		t.Fatal("admin config is enabled without a password")
	}
}

func TestAdminConfigRequiresExternalContent(t *testing.T) {
	t.Setenv(adminPasswordEnvironment, "a-secure-password")
	t.Setenv(adminTrustProxyHTTPSEnvironment, "")
	t.Setenv(adminTrustedProxyCIDRsEnvironment, "")
	t.Setenv("CONTENT_DIR", "")

	config, err := loadAdminConfig()
	if err == nil {
		t.Fatalf("loadAdminConfig returned config %#v without an external content directory", config)
	}
	if !strings.Contains(err.Error(), "CONTENT_DIR") {
		t.Fatalf("error %q does not explain the CONTENT_DIR requirement", err)
	}
}

func TestAdminConfigEnabledWithExternalContent(t *testing.T) {
	t.Setenv(adminPasswordEnvironment, "a-secure-password")
	t.Setenv(adminTrustProxyHTTPSEnvironment, "true")
	t.Setenv(adminTrustedProxyCIDRsEnvironment, "127.0.0.1/32, 10.0.0.0/8")
	t.Setenv("CONTENT_DIR", t.TempDir())

	config, err := loadAdminConfig()
	if err != nil {
		t.Fatalf("loadAdminConfig returned an error: %v", err)
	}
	if !config.Enabled() {
		t.Fatal("admin config is disabled with a password and external content directory")
	}
	if config.Password != "a-secure-password" {
		t.Fatal("admin password was not loaded from the environment")
	}
	if !config.TrustProxyHTTPS {
		t.Fatal("trusted proxy HTTPS setting was not loaded")
	}
	if len(config.TrustedProxyCIDRs) != 2 || config.TrustedProxyCIDRs[1].String() != "10.0.0.0/8" {
		t.Fatalf("trusted proxy CIDRs = %#v", config.TrustedProxyCIDRs)
	}
}

func TestAdminConfigRejectsWeakPassword(t *testing.T) {
	t.Setenv(adminPasswordEnvironment, "too-short")
	t.Setenv(adminTrustProxyHTTPSEnvironment, "")
	t.Setenv(adminTrustedProxyCIDRsEnvironment, "")
	t.Setenv("CONTENT_DIR", t.TempDir())

	_, err := loadAdminConfig()
	if err == nil || !strings.Contains(err.Error(), "at least 16 characters") {
		t.Fatalf("weak password error = %v", err)
	}
}

func TestAdminConfigRejectsInvalidProxySetting(t *testing.T) {
	t.Setenv(adminPasswordEnvironment, "")
	t.Setenv(adminTrustProxyHTTPSEnvironment, "sometimes")
	t.Setenv(adminTrustedProxyCIDRsEnvironment, "")

	_, err := loadAdminConfig()
	if err == nil || !strings.Contains(err.Error(), adminTrustProxyHTTPSEnvironment) {
		t.Fatalf("proxy setting error = %v", err)
	}
}

func TestAdminConfigRequiresValidTrustedProxyCIDRs(t *testing.T) {
	t.Setenv(adminPasswordEnvironment, "a-secure-password")
	t.Setenv(adminTrustProxyHTTPSEnvironment, "true")
	t.Setenv(adminTrustedProxyCIDRsEnvironment, "")
	t.Setenv("CONTENT_DIR", t.TempDir())
	if _, err := loadAdminConfig(); err == nil || !strings.Contains(err.Error(), adminTrustedProxyCIDRsEnvironment) {
		t.Fatalf("missing CIDR error = %v", err)
	}

	t.Setenv(adminTrustedProxyCIDRsEnvironment, "not-a-cidr")
	if _, err := loadAdminConfig(); err == nil || !strings.Contains(err.Error(), "invalid CIDR") {
		t.Fatalf("invalid CIDR error = %v", err)
	}
}

func TestAdminContentWriteCheckIsRequiredOnlyWhenEnabled(t *testing.T) {
	readOnly := fstest.MapFS{"events.yaml": {Data: []byte("events: []\n")}}
	if err := verifyAdminContentWritable(adminConfig{}, readOnly); err != nil {
		t.Fatalf("disabled admin write check returned %v", err)
	}
	if err := verifyAdminContentWritable(adminConfig{Password: "a-secure-password"}, readOnly); err == nil {
		t.Fatal("enabled admin accepted a read-only content filesystem")
	}
}
