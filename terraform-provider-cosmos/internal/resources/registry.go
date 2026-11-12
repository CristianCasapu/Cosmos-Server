package resources

import (
	"context"
	"net/http"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &registryResource{}
	_ resource.ResourceWithImportState = &registryResource{}
)

func NewRegistryResource() resource.Resource {
	return &registryResource{}
}

type registryResource struct {
	client *client.CosmosClient
}

type registryModel struct {
	Name               types.String `tfsdk:"name"`
	Type               types.String `tfsdk:"type"`
	Host               types.String `tfsdk:"host"`
	Internal           types.Bool   `tfsdk:"internal"`
	AllowAnonymousPull types.Bool   `tfsdk:"allow_anonymous_pull"`
	QuotaBytes         types.Int64  `tfsdk:"quota_bytes"`
	Tags               types.Set    `tfsdk:"tags"`
	StorageBackend     types.String `tfsdk:"storage_backend"`
	StorageSeaweedFS   types.String `tfsdk:"storage_seaweedfs"`
	StorageBucket      types.String `tfsdk:"storage_bucket"`
	StorageEndpoint    types.String `tfsdk:"storage_endpoint"`
	StorageRegion      types.String `tfsdk:"storage_region"`
	StorageAccessKey   types.String `tfsdk:"storage_access_key"`
	StorageSecretKey   types.String `tfsdk:"storage_secret_key"`
	StoragePath        types.String `tfsdk:"storage_path"`
	Route              types.String `tfsdk:"route"`
	PurgeDataOnDestroy types.Bool   `tfsdk:"purge_data_on_destroy"`
	Status             types.String `tfsdk:"status"`
}

// registryJSON is the part of pro.RegistryStatus the provider reads back.
type registryJSON struct {
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Host               string   `json:"host"`
	Internal           bool     `json:"internal"`
	AllowAnonymousPull bool     `json:"allowAnonymousPull"`
	QuotaBytes         int64    `json:"quotaBytes"`
	Tags               []string `json:"tags"`
	Status             string   `json:"status"`
	Storage            struct {
		Backend   string `json:"backend"`
		SeaweedFS string `json:"seaweedfs"`
		Bucket    string `json:"bucket"`
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Path      string `json:"path"`
	} `json:"storage"`
	Tokens []struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	} `json:"tokens"`
}

func (r *registryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_registry"
}

func (r *registryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Manages a Cosmos package registry (docker, npm, pypi, static sites or generic files). Pro feature.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Unique registry name (3-27 alphanumeric chars). Used as the resource ID.",
				Required:      true,
				PlanModifiers: replace,
			},
			"type": schema.StringAttribute{
				Description:   "Registry type: docker, npm, static, generic or pypi. Immutable.",
				Required:      true,
				PlanModifiers: replace,
			},
			"host": schema.StringAttribute{
				Description: "Hostname the registry is served on. Not used by static registries, which publish their sites instead.",
				Optional:    true,
			},
			"internal": schema.BoolAttribute{
				Description: "Restrict the registry to the constellation.",
				Optional:    true,
				Computed:    true,
			},
			"allow_anonymous_pull": schema.BoolAttribute{
				Description: "Allow pulls without a token.",
				Optional:    true,
				Computed:    true,
			},
			"quota_bytes": schema.Int64Attribute{
				Description: "Storage quota in bytes. 0 means unlimited.",
				Optional:    true,
				Computed:    true,
			},
			"tags": schema.SetAttribute{
				Description: "Node tags selecting the nodes that serve the registry. Empty means every node.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"route": schema.StringAttribute{
				Description: routeJSONDescription + " Its Host and RestrictToConstellation win over host and internal when set.",
				Optional:    true,
			},
			"storage_backend": schema.StringAttribute{
				Description:   "Where blobs are stored: seaweedfs (a managed object storage instance), s3 (external) or local. Immutable.",
				Required:      true,
				PlanModifiers: replace,
			},
			"storage_seaweedfs": schema.StringAttribute{
				Description:   "Managed object storage instance name, when storage_backend is seaweedfs.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"storage_bucket": schema.StringAttribute{
				Description:   "Bucket name. Generated for the seaweedfs backend.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"storage_endpoint": schema.StringAttribute{
				Description:   "S3 endpoint, when storage_backend is s3.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"storage_region": schema.StringAttribute{
				Description:   "S3 region, when storage_backend is s3.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"storage_access_key": schema.StringAttribute{
				Description:   "S3 access key, when storage_backend is s3.",
				Optional:      true,
				Sensitive:     true,
				PlanModifiers: replace,
			},
			"storage_secret_key": schema.StringAttribute{
				Description:   "S3 secret key, when storage_backend is s3.",
				Optional:      true,
				Sensitive:     true,
				PlanModifiers: replace,
			},
			"storage_path": schema.StringAttribute{
				Description:   "Filesystem root, when storage_backend is local.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"purge_data_on_destroy": schema.BoolAttribute{
				Description: "Also delete every stored blob when the registry is destroyed. Defaults to false: the data is preserved.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"status": schema.StringAttribute{
				Description: "Registry status reported by the server.",
				Computed:    true,
			},
		},
	}
}

func (r *registryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

// populateComputed fills what the server decides after a create/update, leaving
// the configured attributes exactly as planned (Terraform rejects an apply whose
// result differs from the plan).
func (r *registryResource) populateComputed(m *registryModel, reg *registryJSON) {
	if !isSet(m.Internal) {
		m.Internal = types.BoolValue(reg.Internal)
	}
	if !isSet(m.AllowAnonymousPull) {
		m.AllowAnonymousPull = types.BoolValue(reg.AllowAnonymousPull)
	}
	if !isSet(m.QuotaBytes) {
		m.QuotaBytes = types.Int64Value(reg.QuotaBytes)
	}
	if !isSet(m.StorageBucket) {
		m.StorageBucket = types.StringValue(reg.Storage.Bucket)
	}
	m.Status = types.StringValue(reg.Status)
}

func (r *registryResource) populateState(m *registryModel, reg *registryJSON) {
	m.Name = types.StringValue(reg.Name)
	m.Type = types.StringValue(reg.Type)
	m.Host = stringOrNull(reg.Host)
	m.Internal = types.BoolValue(reg.Internal)
	m.AllowAnonymousPull = types.BoolValue(reg.AllowAnonymousPull)
	m.QuotaBytes = types.Int64Value(reg.QuotaBytes)
	m.StorageBackend = types.StringValue(reg.Storage.Backend)
	m.StorageSeaweedFS = stringOrNull(reg.Storage.SeaweedFS)
	m.StorageBucket = types.StringValue(reg.Storage.Bucket)
	m.StorageEndpoint = stringOrNull(reg.Storage.Endpoint)
	m.StorageRegion = stringOrNull(reg.Storage.Region)
	m.StoragePath = stringOrNull(reg.Storage.Path)
	m.Status = types.StringValue(reg.Status)
	// The storage credentials are redacted by the API: the configured values stay in state.
}

func (r *registryResource) read(ctx context.Context, m *registryModel) (*registryJSON, error) {
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationRegistriesName(ctx, m.Name.ValueString())
	})
	if err != nil {
		return nil, err
	}
	return client.ParseResponse[registryJSON](httpResp)
}

func (r *registryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan registryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := cosmossdk.ProRegistryCreateRequest{
		Name:               optString(plan.Name),
		Type:               optString(plan.Type),
		Host:               optString(plan.Host),
		Internal:           optBool(plan.Internal),
		AllowAnonymousPull: optBool(plan.AllowAnonymousPull),
		QuotaBytes:         optInt(plan.QuotaBytes),
		Tags:               optStrings(ctx, plan.Tags, &resp.Diagnostics),
		Route:              parseRouteJSON(plan.Route, &resp.Diagnostics),
		Storage: &cosmossdk.ProRegistryStorage{
			Backend:   cosmossdk.ProRegistryStorageBackend(plan.StorageBackend.ValueString()),
			Seaweedfs: optString(plan.StorageSeaweedFS),
			Bucket:    optString(plan.StorageBucket),
			Endpoint:  optString(plan.StorageEndpoint),
			Region:    optString(plan.StorageRegion),
			AccessKey: optString(plan.StorageAccessKey),
			SecretKey: optString(plan.StorageSecretKey),
			Path:      optString(plan.StoragePath),
		},
	}
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationRegistries(ctx, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating registry", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error creating registry", err.Error())
		return
	}

	reg, err := r.read(ctx, &plan)
	if err != nil || reg == nil {
		resp.Diagnostics.AddError("Error reading registry after create", errString(err))
		return
	}
	r.populateComputed(&plan, reg)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *registryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state registryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	reg, err := r.read(ctx, &state)
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading registry", err.Error())
		return
	}
	if reg == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	r.populateState(&state, reg)
	state.Tags = stringSetOrNull(ctx, reg.Tags, &resp.Diagnostics)
	if state.PurgeDataOnDestroy.IsNull() {
		state.PurgeDataOnDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *registryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan registryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Tags and host are always sent so that removing them from the
	// configuration clears them server-side (absent fields keep their value).
	tags := []string{}
	if t := optStrings(ctx, plan.Tags, &resp.Diagnostics); t != nil {
		tags = *t
	}
	host := plan.Host.ValueString()
	body := cosmossdk.ProRegistrySettingsRequest{
		Host:               &host,
		Internal:           optBool(plan.Internal),
		AllowAnonymousPull: optBool(plan.AllowAnonymousPull),
		QuotaBytes:         optInt(plan.QuotaBytes),
		Tags:               &tags,
		Route:              parseRouteJSON(plan.Route, &resp.Diagnostics),
	}
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PutApiConstellationRegistriesNameSettings(ctx, plan.Name.ValueString(), body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error updating registry", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error updating registry", err.Error())
		return
	}

	reg, err := r.read(ctx, &plan)
	if err != nil || reg == nil {
		resp.Diagnostics.AddError("Error reading registry after update", errString(err))
		return
	}
	r.populateComputed(&plan, reg)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *registryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state registryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := &cosmossdk.DeleteApiConstellationRegistriesNameParams{}
	if state.PurgeDataOnDestroy.ValueBool() {
		params.PurgeData = client.BoolPtr(true)
	}
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationRegistriesName(ctx, state.Name.ValueString(), params)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting registry", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting registry", err.Error())
	}
}

func (r *registryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// errString renders an error that may be nil (an empty API payload).
func errString(err error) string {
	if err == nil {
		return "API returned no data"
	}
	return err.Error()
}
