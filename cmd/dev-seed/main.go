// Command dev-seed populates the development MongoDB with a base commercial
// hierarchy that makes GET /v1/plans and tenant-aware checkout functional:
//
//	organization  Estaleiro de Negocios  (slug: edn-core)
//	  tenant      Funcionario Online     (slug: fun-onl)
//	    catalog   Portal RH Básico       (item_uuid: seed-item-fun-onl-portal-001)
//	    catalog   Suporte Dedicado       (item_uuid: seed-item-fun-onl-suporte-001)
//	    package   Plano Essencial        (package_uuid: seed-pkg-fun-onl-essencial-v1)
//	    plan      Essencial Mensal       (monthly,  R$ 99,00)
//	    plan      Essencial Anual        (annual,   R$ 990,00, up to 12x)
//	    plan      Teste Mensal R$ 1,00    (monthly,  R$ 1,00   — InfinitePay integration test)
//
// All writes use ReplaceOne + upsert=true filtered on natural keys, so the
// command is safe to run multiple times without creating duplicates.
//
// Usage:
//
//	# via tasks.ps1 (loads .env.local automatically)
//	.\tasks.ps1 seed-dev
//
//	# directly (requires VIL_MONGODB_URI in environment)
//	go run ./cmd/dev-seed
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/villenneve/vil-core/internal/devdata"
	"github.com/villenneve/vil-core/internal/platform/config"
	mongoplat "github.com/villenneve/vil-core/internal/platform/mongodb"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatalf("config load: %v", err)
	}
	if cfg.MongoDB.URI == "" {
		fatalf("VIL_MONGODB_URI is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongoplat.New(ctx, cfg.MongoDB)
	if err != nil {
		fatalf("connect mongodb: %v", err)
	}
	defer client.Disconnect(context.Background()) //nolint:errcheck

	if err := mongoplat.BootstrapTenancyStorage(ctx, client, cfg.MongoDB); err != nil {
		fatalf("bootstrap storage: %v", err)
	}

	if err := seed(ctx, client, cfg.MongoDB); err != nil {
		fatalf("seed: %v", err)
	}

	fmt.Println("seed: done")
}

func seed(ctx context.Context, client *mongo.Client, cfg config.MongoConfig) error {
	db := client.Database(cfg.Database)
	now := time.Now().UTC()

	// Organization
	org := devdata.Organization(now)
	if err := upsertDoc(ctx, db.Collection(cfg.CollectionOrganizations),
		bson.D{{Key: "org_uuid", Value: org.OrgUUID}}, org); err != nil {
		return fmt.Errorf("upsert organization: %w", err)
	}
	fmt.Printf("  org       %s  (slug: %s)\n", org.Name, org.Slug)

	// Tenant
	ten := devdata.Tenant(now)
	if err := upsertDoc(ctx, db.Collection(cfg.CollectionTenants),
		bson.D{{Key: "tenant_uuid", Value: ten.TenantUUID}}, ten); err != nil {
		return fmt.Errorf("upsert tenant: %w", err)
	}
	fmt.Printf("  tenant    %s  (slug: %s)\n", ten.Name, ten.Slug)

	// Catalog items
	for _, item := range devdata.CatalogItems(now) {
		item := item
		if err := upsertDoc(ctx, db.Collection(cfg.CollectionCatalogItems),
			bson.D{{Key: "item_uuid", Value: item.ItemUUID}}, item); err != nil {
			return fmt.Errorf("upsert catalog item %s: %w", item.Slug, err)
		}
		fmt.Printf("  item      %s  (slug: %s)\n", item.Name, item.Slug)
	}

	// Package
	pkg := devdata.Package(now)
	if err := upsertDoc(ctx, db.Collection(cfg.CollectionPackages),
		bson.D{{Key: "package_uuid", Value: pkg.PackageUUID}}, pkg); err != nil {
		return fmt.Errorf("upsert package: %w", err)
	}
	fmt.Printf("  package   %s  (slug: %s, v%d)\n", pkg.Name, pkg.Slug, pkg.Version)

	// Plans
	for _, plan := range devdata.Plans(now) {
		plan := plan
		if err := upsertDoc(ctx, db.Collection(cfg.CollectionVersionedPlans),
			bson.D{{Key: "plan_uuid", Value: plan.PlanUUID}}, plan); err != nil {
			return fmt.Errorf("upsert plan %s: %w", plan.Slug, err)
		}
		fmt.Printf("  plan      %s  (slug: %s, cycle: %s, price: %d %s)\n",
			plan.Name, plan.Slug, plan.BillingCycle, plan.PriceCents, plan.Currency)
	}

	return nil
}

// upsertDoc replaces the document matching filter with doc, inserting it if
// no match is found. The natural-key filter guarantees idempotency: reruns
// replace the same document rather than inserting a duplicate.
func upsertDoc(ctx context.Context, coll *mongo.Collection, filter bson.D, doc interface{}) error {
	opts := options.Replace().SetUpsert(true)
	_, err := coll.ReplaceOne(ctx, filter, doc, opts)
	return err
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "seed: "+format+"\n", args...)
	os.Exit(1)
}
