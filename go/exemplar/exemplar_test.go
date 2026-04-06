// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package exemplar_test

import (
	"strings"
	"testing"

	"github.com/marcelocantos/sawmill/exemplar"
)

func TestTemplatizeBasic(t *testing.T) {
	params := map[string]string{
		"name":   "users",
		"entity": "User",
	}

	input := "fn list_users() -> Vec<User> { get_users() }"
	result := exemplar.Templatize(input, params)

	if !strings.Contains(result, "$name") {
		t.Errorf("should replace 'users': %s", result)
	}
	if !strings.Contains(result, "$entity") {
		t.Errorf("should replace 'User': %s", result)
	}
	if strings.Contains(result, "users") {
		t.Errorf("should not contain original 'users': %s", result)
	}
}

func TestTemplatizeCaseVariants(t *testing.T) {
	params := map[string]string{
		"name": "users",
	}

	input := "USERS_TABLE users Users"
	result := exemplar.Templatize(input, params)

	if !strings.Contains(result, "$NAME") {
		t.Errorf("should replace USERS with $NAME: %s", result)
	}
	if !strings.Contains(result, "$name") {
		t.Errorf("should replace users with $name: %s", result)
	}
	if !strings.Contains(result, "$Name") {
		t.Errorf("should replace Users with $Name: %s", result)
	}
}

func TestSubstituteBasic(t *testing.T) {
	params := map[string]string{
		"name":   "products",
		"entity": "Product",
	}

	tmpl := "fn list_$name() -> Vec<$entity> { get_$name() }"
	result := exemplar.Substitute(tmpl, params)

	want := "fn list_products() -> Vec<Product> { get_products() }"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestSubstituteCaseVariants(t *testing.T) {
	params := map[string]string{
		"name": "products",
	}

	tmpl := "$NAME_TABLE $name $Name"
	result := exemplar.Substitute(tmpl, params)

	if !strings.Contains(result, "PRODUCTS") {
		t.Errorf("should expand $NAME to PRODUCTS: %s", result)
	}
	if !strings.Contains(result, "products") {
		t.Errorf("should expand $name to products: %s", result)
	}
	if !strings.Contains(result, "Products") {
		t.Errorf("should expand $Name to Products: %s", result)
	}
}

func TestRoundtrip(t *testing.T) {
	params := map[string]string{
		"name":   "users",
		"entity": "User",
	}

	original := "struct UserHandler {\n    fn list_users(&self) -> Vec<User> {\n        self.db.get_users()\n    }\n}\n"
	tmpl := exemplar.Templatize(original, params)

	newParams := map[string]string{
		"name":   "orders",
		"entity": "Order",
	}

	result := exemplar.Substitute(tmpl, newParams)

	checks := []struct {
		substr string
		desc   string
	}{
		{"OrderHandler", "should have OrderHandler"},
		{"list_orders", "should have list_orders"},
		{"Vec<Order>", "should have Vec<Order>"},
		{"get_orders", "should have get_orders"},
	}
	for _, c := range checks {
		if !strings.Contains(result, c.substr) {
			t.Errorf("%s: %s", c.desc, result)
		}
	}
	if strings.Contains(result, "User") {
		t.Errorf("should not contain 'User': %s", result)
	}
	if strings.Contains(result, "users") {
		t.Errorf("should not contain 'users': %s", result)
	}
}
