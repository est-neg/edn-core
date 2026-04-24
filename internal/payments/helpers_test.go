package payments

import (
	"testing"
)

func TestGenerateOrderNSU(t *testing.T) {
	got1 := GenerateOrderNSU()
	got2 := GenerateOrderNSU()
	if got1 == got2 {
		t.Error("GenerateOrderNSU should generate unique values")
	}
	if len(got1) == 0 {
		t.Error("GenerateOrderNSU should not return empty string")
	}
}

func TestCalculateEventHash(t *testing.T) {
	body := []byte(`{"event":"payment.approved"}`)
	h1 := CalculateEventHash(body)
	h2 := CalculateEventHash(body)
	if h1 != h2 {
		t.Error("CalculateEventHash should be deterministic")
	}
	if len(h1) != 64 {
		t.Errorf("expected SHA-256 hex (64 chars), got %d", len(h1))
	}
	other := CalculateEventHash([]byte(`different`))
	if h1 == other {
		t.Error("different bodies should produce different hashes")
	}
}

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"+5511987654321", "+5511987654321", false},
		{"11987654321", "+5511987654321", false},
		{"11 98765-4321", "+5511987654321", false},
		{"+1 800 555 1234", "+18005551234", false},
		{"abc", "", true},
		{"", "", true},
		{"123", "", true},
	}
	for _, tc := range tests {
		got, err := NormalizePhone(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("NormalizePhone(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeCPF(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"529.982.247-25", "52998224725", false}, // com pontuação
		{"52998224725", "52998224725", false},    // sem pontuação
		{"111.444.777-35", "11144477735", false}, // outro CPF válido
		{"", "", true},                           // vazio
		{"00000000000", "", true},                // all same digit
		{"11111111111", "", true},                // all same digit
		{"12345678900", "", true},                // dígito verificador errado
		{"529.982.247-26", "", true},             // dígito alterado
		{"1234567", "", true},                    // curto demais
	}
	for _, tc := range tests {
		got, err := NormalizeCPF(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("NormalizeCPF(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("NormalizeCPF(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestValidateCreateCheckoutRequest(t *testing.T) {
	valid := CreateCheckoutRequest{
		PlanSlug:     "basic",
		BillingCycle: "monthly",
		Customer: CustomerPayload{
			Name:     "João Silva",
			Email:    "joao@example.com",
			Phone:    "11987654321",
			Document: "529.982.247-25", // CPF válido com pontuação
		},
	}
	if err := ValidateCreateCheckoutRequest(valid); err != nil {
		t.Errorf("expected valid request to pass, got: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CreateCheckoutRequest)
	}{
		{"missing plan_slug", func(r *CreateCheckoutRequest) { r.PlanSlug = "" }},
		{"invalid billing_cycle", func(r *CreateCheckoutRequest) { r.BillingCycle = "weekly" }},
		{"missing customer name", func(r *CreateCheckoutRequest) { r.Customer.Name = "" }},
		{"invalid email", func(r *CreateCheckoutRequest) { r.Customer.Email = "notanemail" }},
		{"invalid phone", func(r *CreateCheckoutRequest) { r.Customer.Phone = "abc" }},
		{"missing document", func(r *CreateCheckoutRequest) { r.Customer.Document = "" }},
		{"invalid document all same digit", func(r *CreateCheckoutRequest) { r.Customer.Document = "00000000000" }},
		{"invalid document wrong check digit", func(r *CreateCheckoutRequest) { r.Customer.Document = "52998224726" }},
	}
	for _, tc := range tests {
		r := valid
		tc.mutate(&r)
		if err := ValidateCreateCheckoutRequest(r); err == nil {
			t.Errorf("case %q: expected validation error but got nil", tc.name)
		}
	}
}
