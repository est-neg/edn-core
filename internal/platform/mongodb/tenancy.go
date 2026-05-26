package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// BootstrapTenancyStorage materializes the collections and indexes for
// organizations, tenants, catalog_items, packages, and idempotency_keys.
// Safe to call on every startup — all operations are idempotent.
func BootstrapTenancyStorage(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	collections := []string{
		cfg.CollectionOrganizations,
		cfg.CollectionTenants,
		cfg.CollectionCatalogItems,
		cfg.CollectionPackages,
		cfg.CollectionIdempotencyKeys,
		cfg.CollectionVersionedPlans,
	}
	for _, name := range collections {
		if err := ensureCollection(ctx, db, name); err != nil {
			return fmt.Errorf("bootstrap tenancy collection %q: %w", name, err)
		}
	}

	return EnsureTenancyIndexes(ctx, client, cfg)
}

// EnsureTenancyIndexes creates all required indexes for tenancy and catalog
// collections idempotently. Safe to call on every startup.
func EnsureTenancyIndexes(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)

	if err := ensureOrganizationsIndexes(ctx, db.Collection(cfg.CollectionOrganizations)); err != nil {
		return err
	}
	if err := ensureTenantsIndexes(ctx, db.Collection(cfg.CollectionTenants)); err != nil {
		return err
	}
	if err := ensureCatalogItemsIndexes(ctx, db.Collection(cfg.CollectionCatalogItems)); err != nil {
		return err
	}
	if err := ensurePackagesIndexes(ctx, db.Collection(cfg.CollectionPackages)); err != nil {
		return err
	}
	if err := ensureIdempotencyKeysIndexes(ctx, db.Collection(cfg.CollectionIdempotencyKeys)); err != nil {
		return err
	}
	if err := ensureVersionedPlansIndexes(ctx, db.Collection(cfg.CollectionVersionedPlans)); err != nil {
		return err
	}
	return nil
}

func ensureOrganizationsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "org_uuid", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_organizations_org_uuid_unique"),
		},
		{
			Keys:    bson.D{{Key: "slug", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_organizations_slug_unique"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure organizations indexes: %w", err)
	}
	return nil
}

func ensureTenantsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "tenant_uuid", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_tenants_tenant_uuid_unique"),
		},
		{
			// Unique: a slug is unique within an organization.
			Keys: bson.D{
				{Key: "organization_id", Value: 1},
				{Key: "slug", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("idx_tenants_org_slug_unique"),
		},
		{
			// Hot-path: resolve active tenants for an organization quickly.
			Keys: bson.D{
				{Key: "organization_id", Value: 1},
				{Key: "active", Value: 1},
			},
			Options: options.Index().SetName("idx_tenants_org_active"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure tenants indexes: %w", err)
	}
	return nil
}

func ensureCatalogItemsIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "item_uuid", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_catalog_items_uuid_unique"),
		},
		{
			// Unique: slug is unique within a tenant.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "slug", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("idx_catalog_items_tenant_slug_unique"),
		},
		{
			// Hot-path: filter by tenant + type + active.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "type", Value: 1},
				{Key: "active", Value: 1},
			},
			Options: options.Index().SetName("idx_catalog_items_tenant_type_active"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure catalog_items indexes: %w", err)
	}
	return nil
}

func ensurePackagesIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "package_uuid", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_packages_uuid_unique"),
		},
		{
			// Unique: (tenant, slug, version) identifies a specific package version.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "slug", Value: 1},
				{Key: "version", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("idx_packages_tenant_slug_version_unique"),
		},
		{
			// Hot-path: find published packages for a tenant by slug.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "slug", Value: 1},
				{Key: "status", Value: 1},
			},
			Options: options.Index().SetName("idx_packages_tenant_slug_status"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure packages indexes: %w", err)
	}
	return nil
}

func ensureIdempotencyKeysIndexes(ctx context.Context, coll *mongo.Collection) error {
	indexes := []mongo.IndexModel{
		{
			// Primary uniqueness: one record per (tenant, operation, key).
			// This prevents two concurrent requests from both passing the check-then-insert.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "operation", Value: 1},
				{Key: "idempotency_key", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("idx_idempotency_keys_tenant_op_key_unique"),
		},
		{
			// TTL index: automatically expire old idempotency records.
			Keys: bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().
				SetExpireAfterSeconds(0).
				SetPartialFilterExpression(bson.D{{Key: "expirable", Value: true}}).
				SetName("idx_idempotency_keys_expires_at_ttl"),
		}, {
			// Non-unique: supports FindByTenantOpResourceID recovery lookup.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "operation", Value: 1},
				{Key: "resource_id", Value: 1},
			},
			Options: options.Index().SetName("idx_idempotency_keys_tenant_op_resource_id"),
		}}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure idempotency_keys indexes: %w", err)
	}
	return nil
}

// dropIndexIfExists drops a named index from coll. It is a no-op when the index
// does not exist (MongoDB code 27) or the collection namespace has not been
// created yet (code 26). All other errors are returned to the caller.
func dropIndexIfExists(ctx context.Context, coll *mongo.Collection, indexName string) error {
	_, err := coll.Indexes().DropOne(ctx, indexName)
	if err == nil {
		return nil
	}
	if cmdErr, ok := err.(mongo.CommandError); ok {
		// 26 = NamespaceNotFound, 27 = IndexNotFound — both are safe to ignore.
		if cmdErr.Code == 26 || cmdErr.Code == 27 {
			return nil
		}
	}
	return err
}

func ensureVersionedPlansIndexes(ctx context.Context, coll *mongo.Collection) error {
	// The legacy unique index lacked billing_cycle, which blocked two plans
	// with the same slug but different billing cycles. Remove it automatically
	// so that BootstrapTenancyStorage is safe to run on existing databases
	// without a prior manual step.
	const legacyIndex = "idx_versioned_plans_tenant_slug_version_unique"
	if err := dropIndexIfExists(ctx, coll, legacyIndex); err != nil {
		return fmt.Errorf("drop legacy versioned_plans index %q: %w", legacyIndex, err)
	}

	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "plan_uuid", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_versioned_plans_uuid_unique"),
		},
		{
			// Unique: a specific version of a (slug, billing_cycle) pair within a tenant is
			// immutable. billing_cycle is part of the key because the same slug is shared
			// across monthly and annual variants of the same commercial offer.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "slug", Value: 1},
				{Key: "billing_cycle", Value: 1},
				{Key: "version", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("idx_versioned_plans_tenant_slug_cycle_version_unique"),
		},
		{
			// Hot-path: resolve active plans by tenant, channel, and billing cycle.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "channel", Value: 1},
				{Key: "active", Value: 1},
				{Key: "billing_cycle", Value: 1},
			},
			Options: options.Index().SetName("idx_versioned_plans_tenant_channel_active_cycle"),
		},
		{
			// Partial index: quickly resolve the active plan for a tenant, slug, and
			// billing cycle. billing_cycle is included because the same slug supports
			// both monthly and annual variants.
			Keys: bson.D{
				{Key: "tenant_id", Value: 1},
				{Key: "slug", Value: 1},
				{Key: "billing_cycle", Value: 1},
			},
			Options: options.Index().
				SetPartialFilterExpression(bson.D{{Key: "active", Value: true}}).
				SetName("idx_versioned_plans_tenant_slug_cycle_active_partial"),
		},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("ensure versioned_plans indexes: %w", err)
	}
	return nil
}
