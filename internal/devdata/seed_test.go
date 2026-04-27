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
		// commercial verticals
		"PackageUUIDAdministrative":     "seed-pkg-fun-onl-administrative-v1",
		"PackageUUIDMedical":            "seed-pkg-fun-onl-medical-v1",
		"PackageUUIDDental":             "seed-pkg-fun-onl-dental-v1",
		"PlanUUIDAdministrativeMonthly": "seed-plan-fun-onl-administrative-mensal-v1",
		"PlanUUIDAdministrativeAnnual":  "seed-plan-fun-onl-administrative-anual-v1",
		"PlanUUIDMedicalMonthly":        "seed-plan-fun-onl-medical-mensal-v1",
		"PlanUUIDMedicalAnnual":         "seed-plan-fun-onl-medical-anual-v1",
		"PlanUUIDDentalMonthly":         "seed-plan-fun-onl-dental-mensal-v1",
		"PlanUUIDDentalAnnual":          "seed-plan-fun-onl-dental-anual-v1",
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
		// commercial verticals
		"PackageUUIDAdministrative":     devdata.PackageUUIDAdministrative,
		"PackageUUIDMedical":            devdata.PackageUUIDMedical,
		"PackageUUIDDental":             devdata.PackageUUIDDental,
		"PlanUUIDAdministrativeMonthly": devdata.PlanUUIDAdministrativeMonthly,
		"PlanUUIDAdministrativeAnnual":  devdata.PlanUUIDAdministrativeAnnual,
		"PlanUUIDMedicalMonthly":        devdata.PlanUUIDMedicalMonthly,
		"PlanUUIDMedicalAnnual":         devdata.PlanUUIDMedicalAnnual,
		"PlanUUIDDentalMonthly":         devdata.PlanUUIDDentalMonthly,
		"PlanUUIDDentalAnnual":          devdata.PlanUUIDDentalAnnual,
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
		Channel      string
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
				Channel      string
			}{p.PlanUUID, p.Slug, p.BillingCycle, p.PriceCents, p.Currency, p.Channel}
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
	if testPlan.Channel != "partner" {
		t.Errorf("Channel = %q, want partner (must not appear on web catalog)", testPlan.Channel)
	}
}

func TestCommercialPackages_CountAndStatus(t *testing.T) {
	pkgs := devdata.CommercialPackages(time.Now())
	if len(pkgs) != 3 {
		t.Fatalf("want 3 commercial packages, got %d", len(pkgs))
	}
	wantSlugs := map[string]bool{"administrative": true, "medical": true, "dental": true}
	for _, pkg := range pkgs {
		if pkg.OrganizationID != devdata.OrgUUID {
			t.Errorf("package %s: OrganizationID mismatch", pkg.Slug)
		}
		if pkg.TenantID != devdata.TenantUUID {
			t.Errorf("package %s: TenantID mismatch", pkg.Slug)
		}
		if pkg.Status != "published" {
			t.Errorf("package %s: Status = %q, want published", pkg.Slug, pkg.Status)
		}
		if !wantSlugs[pkg.Slug] {
			t.Errorf("unexpected package slug %q", pkg.Slug)
		}
		if len(pkg.Items) == 0 {
			t.Errorf("package %s: must have at least one item", pkg.Slug)
		}
	}
}

func TestCommercialPlans_CountAndCycles(t *testing.T) {
	plns := devdata.CommercialPlans(time.Now())
	if len(plns) != 6 {
		t.Fatalf("want 6 commercial plans, got %d", len(plns))
	}
	for _, p := range plns {
		if p.OrganizationID != devdata.OrgUUID {
			t.Errorf("plan %s/%s: OrganizationID mismatch", p.Slug, p.BillingCycle)
		}
		if p.TenantID != devdata.TenantUUID {
			t.Errorf("plan %s/%s: TenantID mismatch", p.Slug, p.BillingCycle)
		}
		if !p.Active {
			t.Errorf("plan %s/%s must be active", p.Slug, p.BillingCycle)
		}
		if p.Channel != "all" {
			t.Errorf("plan %s/%s: Channel = %q, want all", p.Slug, p.BillingCycle, p.Channel)
		}
		if p.PriceCents <= 0 {
			t.Errorf("plan %s/%s: PriceCents must be positive", p.Slug, p.BillingCycle)
		}
		if p.Currency != "BRL" {
			t.Errorf("plan %s/%s: Currency = %q, want BRL", p.Slug, p.BillingCycle, p.Currency)
		}
	}
}

func TestCommercialPlans_SharedSlugPerVertical(t *testing.T) {
	// Each vertical must have exactly one monthly and one annual plan sharing the same slug.
	plns := devdata.CommercialPlans(time.Now())
	type key struct{ slug, cycle string }
	seen := map[key]int{}
	for _, p := range plns {
		seen[key{p.Slug, p.BillingCycle}]++
	}
	for _, slug := range []string{"administrative", "medical", "dental"} {
		for _, cycle := range []string{"monthly", "annual"} {
			k := key{slug, cycle}
			if seen[k] != 1 {
				t.Errorf("slug=%q cycle=%q: want exactly 1 plan, got %d", slug, cycle, seen[k])
			}
		}
	}
}

func TestCommercialPlans_AnnualIsDiscounted(t *testing.T) {
	// Annual price must be 80 % of (monthly × 12).
	plns := devdata.CommercialPlans(time.Now())
	monthly := map[string]int64{}
	annual := map[string]int64{}
	for _, p := range plns {
		switch p.BillingCycle {
		case "monthly":
			monthly[p.Slug] = p.PriceCents
		case "annual":
			annual[p.Slug] = p.PriceCents
		}
	}
	for slug, m := range monthly {
		want := m * 12 * 80 / 100
		if annual[slug] != want {
			t.Errorf("slug=%q: annual price = %d, want %d (monthly %d × 12 × 0.80)",
				slug, annual[slug], want, m)
		}
	}
}
