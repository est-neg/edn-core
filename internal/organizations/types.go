package organizations

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Address represents a postal address.
type Address struct {
	Street     string `bson:"street"      json:"street"`
	Number     string `bson:"number"      json:"number"`
	Complement string `bson:"complement"  json:"complement,omitempty"`
	District   string `bson:"district"    json:"district"`
	City       string `bson:"city"        json:"city"`
	State      string `bson:"state"       json:"state"`
	PostalCode string `bson:"postal_code" json:"postal_code"`
	Country    string `bson:"country"     json:"country"`
}

// Organization is the root administrative and billing entity.
// It owns one or more tenants.
type Organization struct {
	ID           primitive.ObjectID `bson:"_id,omitempty"  json:"-"`
	OrgUUID      string             `bson:"org_uuid"       json:"org_uuid"`
	Slug         string             `bson:"slug"           json:"slug"`
	Name         string             `bson:"name"           json:"name"`
	LegalName    string             `bson:"legal_name"     json:"legal_name"`
	CNPJ         string             `bson:"cnpj"           json:"cnpj,omitempty"`
	BillingEmail string             `bson:"billing_email"  json:"billing_email"`
	Phone        string             `bson:"phone"          json:"phone,omitempty"`
	Address      Address            `bson:"address"        json:"address"`
	Active       bool               `bson:"active"         json:"active"`
	CreatedAt    time.Time          `bson:"created_at"     json:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at"     json:"updated_at"`
}
