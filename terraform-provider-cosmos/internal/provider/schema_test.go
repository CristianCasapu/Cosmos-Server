package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Every resource schema must pass the framework's own implementation checks
// (defaults on non-computed attributes, invalid names...), which otherwise
// only surface when Terraform loads the provider.
func TestResourceSchemas(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "cosmos"}, &meta)
		var resp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &resp)
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Errorf("%s: %v", meta.TypeName, diags)
		}
	}
}
