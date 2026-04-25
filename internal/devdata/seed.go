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
	OrgUUID         = "seed-org-edn-core-001"
	TenantUUID      = "seed-ten-fun-onl-001"
	ItemUUIDPortal  = "seed-item-fun-onl-portal-001"
	ItemUUIDSupport = "seed-item-fun-onl-suporte-001"
	PackageUUID     = "seed-pkg-fun-onl-essencial-v1"
	PlanUUIDMonthly = "seed-plan-fun-onl-essencial-mensal-v1"
	PlanUUIDAnnual  = "seed-plan-fun-onl-essencial-anual-v1"
)

// Organization returns the seed organization document.
func Organization(now time.Time) organizations.Organization {
	return organizations.Organization{
		OrgUUID:      OrgUUID,
		Slug:         "edn-core",
		Name:         "Estaleiro de Negocios",
		BillingEmail: "financeiro@edn-core.com.br",
		Active:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
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

// Plans returns the monthly and annual seed plans pointing to the seed package.
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
	}
}
