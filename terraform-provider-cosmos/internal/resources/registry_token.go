package resources

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &registryTokenResource{}

func NewRegistryTokenResource() resource.Resource {
	return &registryTokenResource{}
}

type registryTokenResource struct {
	client *client.CosmosClient
}

type registryTokenModel struct {
	Registry   types.String `tfsdk:"registry"`
	Name       types.String `tfsdk:"name"`
	Scopes     types.Set    `tfsdk:"scopes"`
	ExpiryDays types.Int64  `tfsdk:"expiry_days"`
	Token      types.String `tfsdk:"token"`
}

func (r *registryTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_registry_token"
}

func (r *registryTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Mints a deploy token on a Cosmos package registry. The raw token only exists in the create response, so every change replaces the token. Pro feature.",
		Attributes: map[string]schema.Attribute{
			"registry": schema.StringAttribute{
				Description:   "Name of the registry the token belongs to.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Description:   "Token name, unique on the registry.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"scopes": schema.SetAttribute{
				Description:   "Token scopes: \"pull\" and/or \"push\", optionally type-qualified (\"docker:push\"). Defaults to pull+push.",
				Optional:      true,
				Computed:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace(), setplanmodifier.UseStateForUnknown()},
			},
			"expiry_days": schema.Int64Attribute{
				Description:   "Days until the token expires. Unset or 0 means never.",
				Optional:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"token": schema.StringAttribute{
				Description:   "The raw token, returned once at creation.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *registryTokenResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

func (r *registryTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan registryTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := cosmossdk.ProRegistryTokenCreateRequest{
		Name:       optString(plan.Name),
		Scopes:     optStrings(ctx, plan.Scopes, &resp.Diagnostics),
		ExpiryDays: optInt(plan.ExpiryDays),
	}
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationRegistriesNameTokens(ctx, plan.Registry.ValueString(), body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating registry token", err.Error())
		return
	}
	created, err := client.ParseResponse[struct {
		Token string `json:"token"`
	}](httpResp)
	if err != nil || created == nil || created.Token == "" {
		resp.Diagnostics.AddError("Error creating registry token", errString(err))
		return
	}
	plan.Token = types.StringValue(created.Token)

	scopes, err := r.readScopes(ctx, &plan)
	if err != nil || scopes == nil {
		resp.Diagnostics.AddError("Error reading registry token after create", errString(err))
		return
	}
	plan.Scopes = stringSetOrNull(ctx, scopes, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// readScopes returns the token's scopes, or nil when the token is gone.
func (r *registryTokenResource) readScopes(ctx context.Context, m *registryTokenModel) ([]string, error) {
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationRegistriesName(ctx, m.Registry.ValueString())
	})
	if err != nil {
		return nil, err
	}
	reg, err := client.ParseResponse[registryJSON](httpResp)
	if err != nil {
		return nil, err
	}
	if reg == nil {
		return nil, nil
	}
	for _, t := range reg.Tokens {
		if strings.EqualFold(t.Name, m.Name.ValueString()) {
			if t.Scopes == nil {
				return []string{}, nil
			}
			return t.Scopes, nil
		}
	}
	return nil, nil
}

func (r *registryTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state registryTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scopes, err := r.readScopes(ctx, &state)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error reading registry token", err.Error())
		return
	}
	if scopes == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Scopes = stringSetOrNull(ctx, scopes, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *registryTokenResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Registry tokens are immutable", "Every attribute of a registry token requires replacement.")
}

func (r *registryTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state registryTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationRegistriesNameTokensTokenName(ctx, state.Registry.ValueString(), state.Name.ValueString())
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting registry token", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting registry token", fmt.Sprintf("%s/%s: %s", state.Registry.ValueString(), state.Name.ValueString(), err))
	}
}
