package config_test

import (
	"testing"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// TestLoad_newCollectionDefaults verifies that all new tenancy/catalog collection
// names are populated with their expected defaults on a zero config.
func TestLoad_newCollectionDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"CollectionOrganizations", cfg.MongoDB.CollectionOrganizations, "organizations"},
		{"CollectionTenants", cfg.MongoDB.CollectionTenants, "tenants"},
		{"CollectionCatalogItems", cfg.MongoDB.CollectionCatalogItems, "catalog_items"},
		{"CollectionPackages", cfg.MongoDB.CollectionPackages, "packages"},
		{"CollectionIdempotencyKeys", cfg.MongoDB.CollectionIdempotencyKeys, "idempotency_keys"},
		{"CollectionVersionedPlans", cfg.MongoDB.CollectionVersionedPlans, "versioned_plans"},
	}

	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestLoad_newCollectionEnvOverride verifies that new collection names can be
// overridden via environment variables.
func TestLoad_newCollectionEnvOverride(t *testing.T) {
	t.Setenv("VIL_MONGODB_COLLECTION_ORGANIZATIONS", "orgs_v2")
	t.Setenv("VIL_MONGODB_COLLECTION_IDEMPOTENCY_KEYS", "idem_keys_v2")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	if cfg.MongoDB.CollectionOrganizations != "orgs_v2" {
		t.Errorf("expected orgs_v2, got %q", cfg.MongoDB.CollectionOrganizations)
	}
	if cfg.MongoDB.CollectionIdempotencyKeys != "idem_keys_v2" {
		t.Errorf("expected idem_keys_v2, got %q", cfg.MongoDB.CollectionIdempotencyKeys)
	}
}
