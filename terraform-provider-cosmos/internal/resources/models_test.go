package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// A model whose tfsdk tags drift from its schema only fails when Terraform
// reads a plan into it, so decode an empty state into each one here.
func TestModelsMatchSchemas(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		res   resource.Resource
		model interface{}
	}{
		{NewDeploymentResource(), &deploymentModel{}},
		{NewRegistryResource(), &registryModel{}},
		{NewRegistryTokenResource(), &registryTokenModel{}},
		{NewDatabaseResource(), &databaseModel{}},
		{NewDatabaseLogicalResource(), &databaseLogicalModel{}},
		{NewObjectStorageResource(), &objectStorageModel{}},
		{NewFunctionResource(), &functionModel{}},
		{NewCIProjectResource(), &ciProjectModel{}},
		{NewDomainResource(), &domainModel{}},
	}
	for _, c := range cases {
		var resp resource.SchemaResponse
		c.res.Schema(ctx, resource.SchemaRequest{}, &resp)

		state := tfsdk.State{Schema: resp.Schema, Raw: emptyObject(ctx, resp)}
		if diags := state.Get(ctx, c.model); diags.HasError() {
			t.Errorf("%T: %v", c.model, diags)
		}
	}
}

func emptyObject(ctx context.Context, resp resource.SchemaResponse) tftypes.Value {
	objType := resp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, attrType := range objType.AttributeTypes {
		values[name] = tftypes.NewValue(attrType, nil)
	}
	return tftypes.NewValue(objType, values)
}
