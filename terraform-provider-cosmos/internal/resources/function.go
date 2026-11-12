package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &functionResource{}
	_ resource.ResourceWithImportState = &functionResource{}
)

func NewFunctionResource() resource.Resource {
	return &functionResource{}
}

type functionResource struct {
	client *client.CosmosClient
}

type functionModel struct {
	Name            types.String  `tfsdk:"name"`
	Deployment      types.String  `tfsdk:"deployment"`
	Runtime         types.String  `tfsdk:"runtime"`
	Image           types.String  `tfsdk:"image"`
	Registry        types.String  `tfsdk:"registry"`
	Package         types.String  `tfsdk:"package"`
	Version         types.String  `tfsdk:"version"`
	Handler         types.String  `tfsdk:"handler"`
	Entry           types.String  `tfsdk:"entry"`
	Env             types.Map     `tfsdk:"env"`
	Tags            types.Set     `tfsdk:"tags"`
	CPU             types.Float64 `tfsdk:"cpu"`
	MemoryMB        types.Int64   `tfsdk:"memory_mb"`
	TimeoutSec      types.Int64   `tfsdk:"timeout_sec"`
	IdleTTL         types.String  `tfsdk:"idle_ttl"`
	Route           types.String  `tfsdk:"route"`
	Triggers        types.String  `tfsdk:"triggers"`
	DeployedVersion types.String  `tfsdk:"deployed_version"`
	Status          types.String  `tfsdk:"status"`
}

func (r *functionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_function"
}

func (r *functionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Cosmos function: a handler served from a package published in a Cosmos npm or pypi registry, scaled from zero. Several functions can share one deployment; use cosmos_deployment with a function block to manage the whole deployment instead. Pro feature.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Unique function name (3-40 alphanumeric chars). Used as the resource ID.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"deployment": schema.StringAttribute{
				Description:   "Deployment carrying the handler. Defaults to \"fn<name>\".",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"runtime": schema.StringAttribute{
				Description: "Runtime, e.g. a node or python version from the server's runtime table.",
				Required:    true,
			},
			"image": schema.StringAttribute{
				Description: "Container image overriding the runtime's default one.",
				Optional:    true,
			},
			"registry": schema.StringAttribute{
				Description: "Cosmos registry holding the package. Its type must match the runtime (npm for node, pypi for python).",
				Required:    true,
			},
			"package": schema.StringAttribute{
				Description: "Package name in that registry.",
				Required:    true,
			},
			"version": schema.StringAttribute{
				Description: "Package version to pin. Unset deploys the registry's latest at creation and leaves later deploys to the server (UI, CI).",
				Optional:    true,
			},
			"handler": schema.StringAttribute{
				Description: "Exported function to call.",
				Required:    true,
			},
			"entry": schema.StringAttribute{
				Description: "File (node) or module (python) exporting the handler. Defaults to the package's main.",
				Optional:    true,
			},
			"env": schema.MapAttribute{
				Description: "Environment variables.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"tags": schema.SetAttribute{
				Description: "Node tags the function may run on. Empty means any node.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"cpu":         schema.Float64Attribute{Description: "CPU limit, in cores.", Optional: true},
			"memory_mb":   schema.Int64Attribute{Description: "Memory limit, in MB.", Optional: true},
			"timeout_sec": schema.Int64Attribute{Description: "Invocation timeout, in seconds.", Optional: true},
			"idle_ttl":    schema.StringAttribute{Description: "How long a replica stays awake without connections (Go duration, e.g. \"5m\").", Optional: true},
			"route": schema.StringAttribute{
				Description: "JSON-encoded utils.ProxyRouteConfig: the user-facing part of the function's URL (host, path, auth, shield...). Use jsonencode() in HCL.",
				Optional:    true,
			},
			"triggers": schema.StringAttribute{
				Description: "JSON-encoded pro.FunctionTriggers, e.g. jsonencode({cron = [{name = \"nightly\", crontab = \"0 0 3 * * *\"}]}).",
				Optional:    true,
			},
			"deployed_version": schema.StringAttribute{
				Description: "Package version currently deployed.",
				Computed:    true,
			},
			"status": schema.StringAttribute{
				Description: "\"created\" (nothing deployed yet) or \"deployed\".",
				Computed:    true,
			},
		},
	}
}

func (r *functionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

// fill writes the configured attributes onto fn. Route, triggers and limits
// are replaced wholesale by the server, so an update starts from the stored
// function and only what the configuration sets is overridden.
func (r *functionResource) fill(ctx context.Context, m *functionModel, fn *cosmossdk.ProFunction, diags *diag.Diagnostics) {
	fn.Name = m.Name.ValueString()
	fn.Deployment = optString(m.Deployment)
	fn.Runtime = m.Runtime.ValueString()
	fn.Image = optString(m.Image)
	fn.Handler = m.Handler.ValueString()
	fn.Entry = optString(m.Entry)
	fn.Env = optStringMap(ctx, m.Env, diags)
	fn.Tags = optStrings(ctx, m.Tags, diags)

	if fn.Source == nil {
		fn.Source = &cosmossdk.ProFunctionSource{}
	}
	fn.Source.Registry = m.Registry.ValueString()
	fn.Source.Package = m.Package.ValueString()
	fn.Source.Token = nil

	limits := cosmossdk.ProFunctionLimits{
		MemoryMB:   optInt(m.MemoryMB),
		TimeoutSec: optInt(m.TimeoutSec),
		IdleTTL:    optString(m.IdleTTL),
	}
	if isSet(m.CPU) {
		limits.Cpu = client.Float32Ptr(float32(m.CPU.ValueFloat64()))
	}
	if limits.Cpu != nil || limits.MemoryMB != nil || limits.TimeoutSec != nil || limits.IdleTTL != nil {
		fn.Limits = &limits
	}

	if route := m.Route.ValueString(); route != "" {
		var parsed cosmossdk.UtilsProxyRouteConfig
		if err := json.Unmarshal([]byte(route), &parsed); err != nil {
			diags.AddError("Invalid function configuration", fmt.Sprintf("parsing route JSON: %s", err))
			return
		}
		fn.Route = &parsed
	}
	if triggers := m.Triggers.ValueString(); triggers != "" {
		var parsed cosmossdk.ProFunctionTriggers
		if err := json.Unmarshal([]byte(triggers), &parsed); err != nil {
			diags.AddError("Invalid function configuration", fmt.Sprintf("parsing triggers JSON: %s", err))
			return
		}
		fn.Triggers = &parsed
	}

	// Server-owned.
	fn.Rev, fn.Releases, fn.Status, fn.Siblings, fn.CreatedAt, fn.UpdatedAt = nil, nil, nil, nil, nil, nil
}

func (r *functionResource) get(ctx context.Context, name string) (*cosmossdk.ProFunction, error) {
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationFunctionsName(ctx, name)
	})
	if err != nil {
		return nil, err
	}
	fn, err := client.ParseResponse[cosmossdk.ProFunction](httpResp)
	if err != nil {
		if client.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return fn, nil
}

func (r *functionResource) setComputed(m *functionModel, fn *cosmossdk.ProFunction) {
	m.Deployment = types.StringValue(client.StringPtrVal(fn.Deployment))
	m.Status = types.StringValue(client.StringPtrVal(fn.Status))
	version := ""
	if fn.Source != nil {
		version = client.StringPtrVal(fn.Source.Version)
	}
	m.DeployedVersion = types.StringValue(version)
}

func (r *functionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan functionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var body cosmossdk.ProFunction
	r.fill(ctx, &plan, &body, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// On create, source.version is the version to deploy (empty = latest).
	body.Source.Version = optString(plan.Version)

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationFunctions(ctx, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating function", err.Error())
		return
	}
	fn, err := client.ParseResponse[cosmossdk.ProFunction](httpResp)
	if err != nil || fn == nil {
		resp.Diagnostics.AddError("Error creating function", errString(err))
		return
	}

	r.setComputed(&plan, fn)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *functionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state functionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fn, err := r.get(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading function", err.Error())
		return
	}
	if fn == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Runtime = types.StringValue(fn.Runtime)
	state.Image = stringOrNull(client.StringPtrVal(fn.Image))
	state.Handler = types.StringValue(fn.Handler)
	state.Entry = stringOrNull(client.StringPtrVal(fn.Entry))
	if fn.Source != nil {
		state.Registry = types.StringValue(fn.Source.Registry)
		// A pinned version drifts when something else deployed another one.
		if isSet(state.Version) {
			state.Version = types.StringValue(client.StringPtrVal(fn.Source.Version))
		}
	}
	if fn.Tags != nil {
		state.Tags = stringSetOrNull(ctx, *fn.Tags, &resp.Diagnostics)
	} else {
		state.Tags = types.SetNull(types.StringType)
	}
	// The package name, limits, route and triggers are normalized server-side
	// and env is merged with the sibling handlers' one, so the configured
	// values stay the source of truth in state.
	r.setComputed(&state, fn)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *functionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state functionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	body, err := r.get(ctx, name)
	if err != nil || body == nil {
		resp.Diagnostics.AddError("Error reading function before update", errString(err))
		return
	}
	r.fill(ctx, &plan, body, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PutApiConstellationFunctionsName(ctx, name, *body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error updating function", err.Error())
		return
	}
	fn, err := client.ParseResponse[cosmossdk.ProFunction](httpResp)
	if err != nil || fn == nil {
		resp.Diagnostics.AddError("Error updating function", errString(err))
		return
	}

	// An update never moves the pinned version: that is what deploy is for.
	if version := optString(plan.Version); version != nil && (fn.Source == nil || client.StringPtrVal(fn.Source.Version) != *version) {
		httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
			return r.client.Raw.PostApiConstellationFunctionsNameDeploy(ctx, name, cosmossdk.ProFunctionDeployRequest{Version: version})
		})
		if err != nil {
			resp.Diagnostics.AddError("Error deploying function", err.Error())
			return
		}
		fn, err = client.ParseResponse[cosmossdk.ProFunction](httpResp)
		if err != nil || fn == nil {
			resp.Diagnostics.AddError("Error deploying function", errString(err))
			return
		}
	}

	r.setComputed(&plan, fn)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *functionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state functionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationFunctionsName(ctx, state.Name.ValueString())
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting function", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting function", err.Error())
	}
}

func (r *functionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
