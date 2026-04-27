// Package devdata provides deterministic seed documents for the development
// environment. All identifiers are stable string constants so that reruns
// produce the same natural keys and upserts remain safe.
package devdata

import (
	"time"

	"github.com/villenneve/vil-core/internal/catalog"
	"github.com/villenneve/vil-core/internal/organizations"
	"github.com/villenneve/vil-core/internal/packages"
	"github.com/villenneve/vil-core/internal/plans"
	"github.com/villenneve/vil-core/internal/tenants"
)

// Stable natural-key identifiers — never change these; they are the upsert filters.
const (
	OrgUUID             = "seed-org-edn-core-001"
	TenantUUID          = "seed-ten-fun-onl-001"
	ItemUUIDPortal      = "seed-item-fun-onl-portal-001"
	ItemUUIDSupport     = "seed-item-fun-onl-suporte-001"
	PackageUUID         = "seed-pkg-fun-onl-essencial-v1"
	PlanUUIDMonthly     = "seed-plan-fun-onl-essencial-mensal-v1"
	PlanUUIDAnnual      = "seed-plan-fun-onl-essencial-anual-v1"
	PlanUUIDTestOneReal = "seed-plan-fun-onl-teste-1real-v1" // R$ 1,00 — InfinitePay integration test only

	// Commercial vertical packages — slug shared across billing cycles.
	PackageUUIDAdministrative = "seed-pkg-fun-onl-administrative-v1"
	PackageUUIDMedical        = "seed-pkg-fun-onl-medical-v1"
	PackageUUIDDental         = "seed-pkg-fun-onl-dental-v1"

	// Commercial vertical plans — monthly/annual pairs share the vertical slug.
	PlanUUIDAdministrativeMonthly = "seed-plan-fun-onl-administrative-mensal-v1"
	PlanUUIDAdministrativeAnnual  = "seed-plan-fun-onl-administrative-anual-v1"
	PlanUUIDMedicalMonthly        = "seed-plan-fun-onl-medical-mensal-v1"
	PlanUUIDMedicalAnnual         = "seed-plan-fun-onl-medical-anual-v1"
	PlanUUIDDentalMonthly         = "seed-plan-fun-onl-dental-mensal-v1"
	PlanUUIDDentalAnnual          = "seed-plan-fun-onl-dental-anual-v1"
)

// Organization returns the seed organization document.
func Organization(now time.Time) organizations.Organization {
	return organizations.Organization{
		OrgUUID:      OrgUUID,
		Slug:         "edn-core",
		Name:         "Estaleiro de Negocios",
		LegalName:    "ESTALEIRO DE NEGOCIOS LTDA",
		CNPJ:         "62.124.197/0001-28",
		BillingEmail: "gillylopes@gmail.com",
		Phone:        "(69) 9206-0958",
		Address: organizations.Address{
			Street:     "R ANTONIO SAAD",
			Number:     "2500",
			Complement: "COND TERRA NOVA CASA 395",
			District:   "BOA VISTA",
			City:       "PONTA GROSSA",
			State:      "PR",
			PostalCode: "84073-170",
			Country:    "BR",
		},
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Tenant returns the seed tenant document.
func Tenant(now time.Time) tenants.Tenant {
	return tenants.Tenant{
		TenantUUID:     TenantUUID,
		OrganizationID: OrgUUID,
		Slug:           "fun-onl",
		Name:           "Funcionario Online",
		Channels:       []string{"web", "mobile"},
		Active:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// CatalogItems returns the two seed catalog items for the tenant.
func CatalogItems(now time.Time) []catalog.Item {
	return []catalog.Item{
		{
			ItemUUID:       ItemUUIDPortal,
			OrganizationID: OrgUUID,
			TenantID:       TenantUUID,
			Type:           "service",
			Slug:           "portal-rh-basico",
			Name:           "Portal RH Básico",
			Description:    "Acesso ao portal de gestão de pessoal e folha de pagamento",
			DeliveryMode:   "api_grant",
			Active:         true,
			Metadata:       map[string]interface{}{"feature_tier": "basic"},
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ItemUUID:       ItemUUIDSupport,
			OrganizationID: OrgUUID,
			TenantID:       TenantUUID,
			Type:           "service",
			Slug:           "suporte-dedicado",
			Name:           "Suporte Dedicado",
			Description:    "Atendimento por e-mail e chat em dias úteis das 8h às 18h",
			DeliveryMode:   "api_grant",
			Active:         true,
			Metadata:       map[string]interface{}{"sla_hours": 24},
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}

// Package returns the seed package document composed from the two catalog items.
func Package(now time.Time) packages.Package {
	return packages.Package{
		PackageUUID:    PackageUUID,
		OrganizationID: OrgUUID,
		TenantID:       TenantUUID,
		Slug:           "essencial-v1",
		Version:        1,
		Name:           "Plano Essencial",
		Status:         "published",
		Items: []packages.PackageItem{
			{
				ItemUUID:     ItemUUIDPortal,
				ItemType:     "service",
				ItemSlug:     "portal-rh-basico",
				ItemName:     "Portal RH Básico",
				Quantity:     1,
				DeliveryMode: "api_grant",
			},
			{
				ItemUUID:     ItemUUIDSupport,
				ItemType:     "service",
				ItemSlug:     "suporte-dedicado",
				ItemName:     "Suporte Dedicado",
				Quantity:     1,
				DeliveryMode: "api_grant",
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Plans returns the seed plans pointing to the seed package.
// The slice includes the two commercial plans (monthly/annual) plus a
// R$ 1,00 monthly plan used exclusively for InfinitePay integration tests.
func Plans(now time.Time) []plans.Plan {
	pkgRef := plans.PackageRef{PackageUUID: PackageUUID, Version: 1}
	pkgSnap := plans.PackageSnapshot{
		PackageUUID: PackageUUID,
		Name:        "Plano Essencial",
		Version:     1,
	}

	return []plans.Plan{
		{
			PlanUUID:        PlanUUIDMonthly,
			OrganizationID:  OrgUUID,
			TenantID:        TenantUUID,
			Slug:            "essencial-mensal-v1",
			Version:         1,
			Name:            "Essencial Mensal",
			PackageRef:      pkgRef,
			PackageSnapshot: pkgSnap,
			BillingCycle:    plans.BillingCycleMonthly,
			PriceCents:      9900, // R$ 99,00
			Currency:        "BRL",
			MaxInstallments: 1,
			Channel:         plans.ChannelAll,
			Active:          true,
			ValidFrom:       now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			PlanUUID:        PlanUUIDAnnual,
			OrganizationID:  OrgUUID,
			TenantID:        TenantUUID,
			Slug:            "essencial-anual-v1",
			Version:         1,
			Name:            "Essencial Anual",
			PackageRef:      pkgRef,
			PackageSnapshot: pkgSnap,
			BillingCycle:    plans.BillingCycleAnnual,
			PriceCents:      99000, // R$ 990,00 (~R$ 82,50/mês)
			Currency:        "BRL",
			MaxInstallments: 12,
			Channel:         plans.ChannelAll,
			Active:          true,
			ValidFrom:       now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			PlanUUID:        PlanUUIDTestOneReal,
			OrganizationID:  OrgUUID,
			TenantID:        TenantUUID,
			Slug:            "teste-mensal-1real-v1",
			Version:         1,
			Name:            "Teste Mensal R$ 1,00",
			PackageRef:      pkgRef,
			PackageSnapshot: pkgSnap,
			BillingCycle:    plans.BillingCycleMonthly,
			PriceCents:      100, // R$ 1,00 — low-value monthly plan for InfinitePay integration tests
			Currency:        "BRL",
			MaxInstallments: 1,
			Channel:         plans.ChannelPartner, // not visible on web catalog
			Active:          true,
			ValidFrom:       now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}
}

// CommercialPackages returns the three commercial vertical packages for dev seed.
// Each package references the same catalog items as the legacy essencial package —
// full item differentiation per vertical is out of scope for the seed layer.
func CommercialPackages(now time.Time) []packages.Package {
	items := []packages.PackageItem{
		{
			ItemUUID:     ItemUUIDPortal,
			ItemType:     "service",
			ItemSlug:     "portal-rh-basico",
			ItemName:     "Portal RH Básico",
			Quantity:     1,
			DeliveryMode: "api_grant",
		},
		{
			ItemUUID:     ItemUUIDSupport,
			ItemType:     "service",
			ItemSlug:     "suporte-dedicado",
			ItemName:     "Suporte Dedicado",
			Quantity:     1,
			DeliveryMode: "api_grant",
		},
	}
	return []packages.Package{
		{
			PackageUUID:    PackageUUIDAdministrative,
			OrganizationID: OrgUUID,
			TenantID:       TenantUUID,
			Slug:           "administrative",
			Version:        1,
			Name:           "Administrativo",
			Status:         "published",
			Items:          items,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			PackageUUID:    PackageUUIDMedical,
			OrganizationID: OrgUUID,
			TenantID:       TenantUUID,
			Slug:           "medical",
			Version:        1,
			Name:           "Médico",
			Status:         "published",
			Items:          items,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			PackageUUID:    PackageUUIDDental,
			OrganizationID: OrgUUID,
			TenantID:       TenantUUID,
			Slug:           "dental",
			Version:        1,
			Name:           "Odontológico",
			Status:         "published",
			Items:          items,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}

// CommercialPlans returns the six versioned plans for the three commercial verticals.
// Each vertical has a monthly and an annual variant sharing the same slug —
// the billing_cycle field is the disambiguator in the unique index.
// Annual price = monthly × 12 × 0.80 (20 % off).
func CommercialPlans(now time.Time) []plans.Plan {
	type entry struct {
		planUUIDMonthly string
		planUUIDAnnual  string
		pkgUUID         string
		pkgName         string
		slug            string
		name            string
		monthlyPrice    int64
	}
	verticals := []entry{
		{
			planUUIDMonthly: PlanUUIDAdministrativeMonthly,
			planUUIDAnnual:  PlanUUIDAdministrativeAnnual,
			pkgUUID:         PackageUUIDAdministrative,
			pkgName:         "Administrativo",
			slug:            "administrative",
			name:            "Administrativo",
			monthlyPrice:    14900, // R$ 149,00
		},
		{
			planUUIDMonthly: PlanUUIDMedicalMonthly,
			planUUIDAnnual:  PlanUUIDMedicalAnnual,
			pkgUUID:         PackageUUIDMedical,
			pkgName:         "Médico",
			slug:            "medical",
			name:            "Médico",
			monthlyPrice:    24900, // R$ 249,00
		},
		{
			planUUIDMonthly: PlanUUIDDentalMonthly,
			planUUIDAnnual:  PlanUUIDDentalAnnual,
			pkgUUID:         PackageUUIDDental,
			pkgName:         "Odontológico",
			slug:            "dental",
			name:            "Odontológico",
			monthlyPrice:    9900, // R$ 99,00
		},
	}

	var out []plans.Plan
	for _, v := range verticals {
		pkgRef := plans.PackageRef{PackageUUID: v.pkgUUID, Version: 1}
		pkgSnap := plans.PackageSnapshot{PackageUUID: v.pkgUUID, Name: v.pkgName, Version: 1}
		annualPrice := v.monthlyPrice * 12 * 80 / 100 // 20 % discount

		out = append(out,
			plans.Plan{
				PlanUUID:        v.planUUIDMonthly,
				OrganizationID:  OrgUUID,
				TenantID:        TenantUUID,
				Slug:            v.slug,
				Version:         1,
				Name:            v.name + " Mensal",
				PackageRef:      pkgRef,
				PackageSnapshot: pkgSnap,
				BillingCycle:    plans.BillingCycleMonthly,
				PriceCents:      v.monthlyPrice,
				Currency:        "BRL",
				MaxInstallments: 1,
				Channel:         plans.ChannelAll,
				Active:          true,
				ValidFrom:       now,
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			plans.Plan{
				PlanUUID:        v.planUUIDAnnual,
				OrganizationID:  OrgUUID,
				TenantID:        TenantUUID,
				Slug:            v.slug,
				Version:         1,
				Name:            v.name + " Anual",
				PackageRef:      pkgRef,
				PackageSnapshot: pkgSnap,
				BillingCycle:    plans.BillingCycleAnnual,
				PriceCents:      annualPrice,
				Currency:        "BRL",
				MaxInstallments: 12,
				Channel:         plans.ChannelAll,
				Active:          true,
				ValidFrom:       now,
				CreatedAt:       now,
				UpdatedAt:       now,
			},
		)
	}
	return out
}
