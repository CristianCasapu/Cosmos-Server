package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"

	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Helpers shared by the Constellation feature resources (registries, managed
// databases, object storage, functions, CI projects).

// configureClient extracts the provider client handed to a resource.
func configureClient(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *client.CosmosClient {
	if req.ProviderData == nil {
		return nil
	}
	c, ok := req.ProviderData.(*client.CosmosClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.CosmosClient, got: %T", req.ProviderData),
		)
		return nil
	}
	return c
}

func isSet(v interface {
	IsNull() bool
	IsUnknown() bool
}) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// optString returns nil for a null, unknown or empty string.
func optString(v types.String) *string {
	if !isSet(v) || v.ValueString() == "" {
		return nil
	}
	s := v.ValueString()
	return &s
}

func optBool(v types.Bool) *bool {
	if !isSet(v) {
		return nil
	}
	b := v.ValueBool()
	return &b
}

func optInt(v types.Int64) *int {
	if !isSet(v) {
		return nil
	}
	i := int(v.ValueInt64())
	return &i
}

// optStrings returns nil for a null, unknown or empty set.
func optStrings(ctx context.Context, v types.Set, diags *diag.Diagnostics) *[]string {
	if !isSet(v) {
		return nil
	}
	var out []string
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	if len(out) == 0 {
		return nil
	}
	return &out
}

func optStringMap(ctx context.Context, v types.Map, diags *diag.Diagnostics) *map[string]string {
	if !isSet(v) {
		return nil
	}
	out := map[string]string{}
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	if len(out) == 0 {
		return nil
	}
	return &out
}

// stringOrNull keeps an unset attribute null instead of drifting to "".
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func stringSetOrNull(ctx context.Context, values []string, diags *diag.Diagnostics) types.Set {
	if len(values) == 0 {
		return types.SetNull(types.StringType)
	}
	set, d := types.SetValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return set
}

func stringMapOrNull(ctx context.Context, values map[string]string, diags *diag.Diagnostics) types.Map {
	if len(values) == 0 {
		return types.MapNull(types.StringType)
	}
	m, d := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return m
}

// parseRouteJSON decodes a jsonencode()d utils.ProxyRouteConfig; nil when unset.
func parseRouteJSON(v types.String, diags *diag.Diagnostics) *cosmossdk.UtilsProxyRouteConfig {
	if !isSet(v) || v.ValueString() == "" {
		return nil
	}
	var route cosmossdk.UtilsProxyRouteConfig
	if err := json.Unmarshal([]byte(v.ValueString()), &route); err != nil {
		diags.AddError("Invalid route", fmt.Sprintf("parsing route JSON: %s", err))
		return nil
	}
	return &route
}

const routeJSONDescription = "JSON-encoded utils.ProxyRouteConfig with the user-facing settings of the route (auth, SmartShield, whitelist...). Use jsonencode() in HCL. The name, mode and target are forced by the server. Not refreshed from the server."

// backupModel is the restic backup block shared by managed databases and
// object storage (whose metadata is what gets backed up).
type backupModel struct {
	Enabled         types.Bool   `tfsdk:"enabled"`
	Repository      types.String `tfsdk:"repository"`
	Crontab         types.String `tfsdk:"crontab"`
	CrontabForget   types.String `tfsdk:"crontab_forget"`
	RetentionPolicy types.String `tfsdk:"retention_policy"`
}

func backupSchemaAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: description + " Removing the block clears the configuration; the repository and its snapshots are left untouched. The repository password is minted and kept by the server.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"enabled": schema.BoolAttribute{
				Description: "Whether the scheduled backups run. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"repository": schema.StringAttribute{
				Description: "Restic repository: a path on the node, or an rclone remote path.",
				Required:    true,
			},
			"crontab": schema.StringAttribute{
				Description: "Backup schedule (6-field crontab, seconds first). Server default when unset.",
				Optional:    true,
			},
			"crontab_forget": schema.StringAttribute{
				Description: "Schedule of the retention pass (6-field crontab). Server default when unset.",
				Optional:    true,
			},
			"retention_policy": schema.StringAttribute{
				Description: "Restic forget flags, e.g. \"--keep-daily 7 --keep-weekly 4\". Server default when unset.",
				Optional:    true,
			},
		},
	}
}

// syncBackup brings the server's backup configuration in line with the plan.
func syncBackup(plan, state *backupModel, put func(cosmossdk.ProManagedDBBackupRequest) (*http.Response, error), del func() (*http.Response, error)) error {
	if plan == nil {
		if state == nil {
			return nil
		}
		resp, err := del()
		if err != nil {
			return err
		}
		if err := client.CheckResponse(resp); err != nil && !client.IsNotFound(err) {
			return err
		}
		return nil
	}
	if state != nil && *plan == *state {
		return nil
	}
	resp, err := put(cosmossdk.ProManagedDBBackupRequest{
		Enabled:         client.BoolPtr(!isSet(plan.Enabled) || plan.Enabled.ValueBool()),
		Repository:      optString(plan.Repository),
		Crontab:         optString(plan.Crontab),
		CrontabForget:   optString(plan.CrontabForget),
		RetentionPolicy: optString(plan.RetentionPolicy),
	})
	if err != nil {
		return err
	}
	return client.CheckResponse(resp)
}
