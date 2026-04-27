package devdata_test

import (
	"testing"
	"time"

	"github.com/villenneve/vil-core/internal/devdata"
)

func TestOrganization_NaturalKey(t *testing.T) {
	org := devdata.Organization(time.Now())
	if org.OrgUUID != devdata.OrgUUID {
		t.Errorf("OrgUUID = %q, want %q", org.OrgUUID, devdata.OrgUUID)
	}
	if org.Slug != "edn-core" {
		t.Errorf("Slug = %q, want %q", org.Slug, "edn-core")
	}
	if org.Name != "Estaleiro de Negocios" {
		t.Errorf("Name = %q, want %q", org.Name, "Estaleiro de Negocios")
	}
	if !org.Active {
		t.Error("Organization must be active")
	}
}

func TestTenant_NaturalKeyAndRelation(t *testing.T) {
	ten := devdata.Tenant(time.Now())
	if ten.TenantUUID != devdata.TenantUUID {
		t.Errorf("TenantUUID = %q, want %q", ten.TenantUUID, devdata.TenantUUID)
	}
	if ten.Slug != "fun-onl" {
		t.Errorf("Slug = %q, want %q", ten.Slug, "fun-onl")
	}
	if ten.OrganizationID != devdata.OrgUUID {
		t.Errorf("OrganizationID = %q, want OrgUUID %q", ten.OrganizationID, devdata.OrgUUID)
	}
	if !ten.Active {
		t.Error("Tenant must be active")
	}
	if len(ten.Channels) == 0 {
		t.Error("Tenant must have at least one channel")
	}
}

func TestCatalogItems_ConsistencyAndCount(t *testing.T) {
	items := devdata.CatalogItems(time.Now())
	if len(items) != 2 {
		t.Fatalf("want 2 catalog items, got %d", len(items))
	}

	for _, item := range items {
		if item.OrganizationID != devdata.OrgUUID {
			t.Errorf("item %s: OrganizationID = %q, want %q", item.Slug, item.OrganizationID, devdata.OrgUUID)
		}
		if item.TenantID != devdata.TenantUUID {
			t.Errorf("item %s: TenantID = %q, want %q", item.Slug, item.TenantID, devdata.TenantUUID)
		}
		if !item.Active {
			t.Errorf("item %s must be active", item.Slug)
		}
		if item.ItemUUID == "" {
			t.Errorf("item %s: ItemUUID must not be empty", item.Slug)
		}
	}
}

func TestPackage_ItemsMatchCatalog(t *testing.T) {
	pkg := devdata.Package(time.Now())
	if pkg.PackageUUID != devdata.PackageUUID {
		t.Errorf("PackageUUID = %q, want %q", pkg.PackageUUID, devdata.PackageUUID)
	}
	if pkg.OrganizationID != devdata.OrgUUID {
		t.Errorf("OrganizationID = %q, want %q", pkg.OrganizationID, devdata.OrgUUID)
	}
	if pkg.TenantID != devdata.TenantUUID {
		t.Errorf("TenantID = %q, want %q", pkg.TenantID, devdata.TenantUUID)
	}
	if pkg.Status != "published" {
		t.Errorf("Status = %q, want published", pkg.Status)
	}
	if len(pkg.Items) != 2 {
		t.Fatalf("want 2 package items, got %d", len(pkg.Items))
	}

	catalogUUIDs := map[string]bool{
		devdata.ItemUUIDPortal:  true,
		devdata.ItemUUIDSupport: true,
	}
	for _, pi := range pkg.Items {
		if !catalogUUIDs[pi.ItemUUID] {
			t.Errorf("package item %q is not a known catalog item UUID", pi.ItemUUID)
		}
	}
}

func TestPlans_ReferencePackageAndBothCycles(t *testing.T) {
	plns := devdata.Plans(time.Now())
	if len(plns) != 3 {
		t.Fatalf("want 3 plans, got %d", len(plns))
	}

	cycles := map[string]bool{}
	for _, p := range plns {
		if p.PackageRef.PackageUUID != devdata.PackageUUID {
			t.Errorf("plan %s: PackageRef.PackageUUID = %q, want %q", p.Slug, p.PackageRef.PackageUUID, devdata.PackageUUID)
		}
		if p.PackageSnapshot.PackageUUID != devdata.PackageUUID {
			t.Errorf("plan %s: PackageSnapshot.PackageUUID = %q, want %q", p.Slug, p.PackageSnapshot.PackageUUID, devdata.PackageUUID)
		}
		if p.OrganizationID != devdata.OrgUUID {
			t.Errorf("plan %s: OrganizationID mismatch", p.Slug)
		}
		if p.TenantID != devdata.TenantUUID {
			t.Errorf("plan %s: TenantID mismatch", p.Slug)
		}
		if !p.Active {
			t.Errorf("plan %s must be active", p.Slug)
		}
		if p.PriceCents <= 0 {
			t.Errorf("plan %s: PriceCents must be positive", p.Slug)
		}
		if p.Currency != "BRL" {
			t.Errorf("plan %s: Currency = %q, want BRL", p.Slug, p.Currency)
		}
		cycles[p.BillingCycle] = true
	}

	if !cycles["monthly"] {
		t.Error("missing monthly plan")
	}
	if !cycles["annual"] {
		t.Error("missing annual plan")
	}
}

func TestSeedUUIDs_AreStable(t *testing.T) {
	// Ensures the constants that serve as upsert keys never accidentally change.
	want := map[string]string{
		"OrgUUID":             "seed-org-edn-core-001",
		"TenantUUID":          "seed-ten-fun-onl-001",
		"ItemUUIDPortal":      "seed-item-fun-onl-portal-001",
		"ItemUUIDSupport":     "seed-item-fun-onl-suporte-001",
		"PackageUUID":         "seed-pkg-fun-onl-essencial-v1",
		"PlanUUIDMonthly":     "seed-plan-fun-onl-essencial-mensal-v1",
		"PlanUUIDAnnual":      "seed-plan-fun-onl-essencial-anual-v1",
		"PlanUUIDTestOneReal": "seed-plan-fun-onl-teste-1real-v1",
	}
	got := map[string]string{
		"OrgUUID":             devdata.OrgUUID,
		"TenantUUID":          devdata.TenantUUID,
		"ItemUUIDPortal":      devdata.ItemUUIDPortal,
		"ItemUUIDSupport":     devdata.ItemUUIDSupport,
		"PackageUUID":         devdata.PackageUUID,
		"PlanUUIDMonthly":     devdata.PlanUUIDMonthly,
		"PlanUUIDAnnual":      devdata.PlanUUIDAnnual,
		"PlanUUIDTestOneReal": devdata.PlanUUIDTestOneReal,
	}
	for name, wantVal := range want {
		if got[name] != wantVal {
			t.Errorf("%s = %q, want %q", name, got[name], wantVal)
		}
	}
}

func TestPlanTestOneReal_Fields(t *testing.T) {
	var testPlan *struct {
		PlanUUID     string
		Slug         string
		BillingCycle string
		PriceCents   int64
		Currency     string
	}
	for _, p := range devdata.Plans(time.Now()) {
		p := p
		if p.PlanUUID == devdata.PlanUUIDTestOneReal {
			testPlan = &struct {
				PlanUUID     string
				Slug         string
				BillingCycle string
				PriceCents   int64
				Currency     string
			}{p.PlanUUID, p.Slug, p.BillingCycle, p.PriceCents, p.Currency}
			break
		}
	}
	if testPlan == nil {
		t.Fatal("PlanUUIDTestOneReal not found in Plans()")
	}
	if testPlan.Slug != "teste-mensal-1real-v1" {
		t.Errorf("Slug = %q, want teste-mensal-1real-v1", testPlan.Slug)
	}
	if testPlan.BillingCycle != "monthly" {
		t.Errorf("BillingCycle = %q, want monthly", testPlan.BillingCycle)
	}
	if testPlan.PriceCents != 100 {
		t.Errorf("PriceCents = %d, want 100 (R$ 1,00)", testPlan.PriceCents)
	}
	if testPlan.Currency != "BRL" {
		t.Errorf("Currency = %q, want BRL", testPlan.Currency)
	}
}
