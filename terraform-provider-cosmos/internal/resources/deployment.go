package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// retryOn503 retries an API call while the server returns 503. The
// constellation-deployments KV is created asynchronously by
// pro.ClientHeartbeatInit after the NATS cluster forms, which lags
// cosmos_install completion by ~30-60s. The deployments endpoints are the
// only ones gated on that KV, so the wait belongs here rather than in
// cosmos_install. On final timeout we return the last 503 response so the
// parser surfaces the real DP001 error.
func retryOn503(ctx context.Context, do func() (*http.Response, error)) (*http.Response, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		resp, err := do()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusServiceUnavailable {
			return resp, nil
		}
		if time.Now().After(deadline) {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

var (
	_ resource.Resource                = &deploymentResource{}
	_ resource.ResourceWithImportState = &deploymentResource{}
)

func NewDeploymentResource() resource.Resource {
	return &deploymentResource{}
}

type deploymentResource struct {
	client *client.CosmosClient
}

type deploymentModel struct {
	Name            types.String `tfsdk:"name"`
	Replicas        types.Int64  `tfsdk:"replicas"`
	MinReplicas     types.Int64  `tfsdk:"min_replicas"`
	MaxReplicas     types.Int64  `tfsdk:"max_replicas"`
	ReplicaFill     types.Bool   `tfsdk:"replica_fill"`
	ReplicaFillMode types.String `tfsdk:"replica_fill_mode"`
	Strategy        types.String `tfsdk:"strategy"`
	Tags            types.Set    `tfsdk:"tags"`
	Storage         types.Set    `tfsdk:"storage"`
	Compose         types.String `tfsdk:"compose"`
	Function        types.String `tfsdk:"function"`
	Version         types.Int64  `tfsdk:"version"`
}

func (r *deploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func (r *deploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Cosmos cluster deployment. Deployments are replicated across the constellation, so this resource can target any node in the cluster.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description: "Unique deployment name (3-64 alphanumeric chars). Used as the resource ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"replicas": schema.Int64Attribute{
				Description: "Fixed replica count. Exactly one replica mode must be set: replicas (fixed), min_replicas/max_replicas (autoscale) or replica_fill (one per eligible node).",
				Optional:    true,
			},
			"min_replicas": schema.Int64Attribute{
				Description: "Autoscale mode: lower bound of the load-based replica count. Set together with max_replicas.",
				Optional:    true,
			},
			"max_replicas": schema.Int64Attribute{
				Description: "Autoscale mode: upper bound of the load-based replica count. Set together with min_replicas.",
				Optional:    true,
			},
			"replica_fill": schema.BoolAttribute{
				Description: "Fill mode: one replica on every alive node matching tags (every node when tags is empty).",
				Optional:    true,
			},
			"replica_fill_mode": schema.StringAttribute{
				Description: "Refines fill mode (only with replica_fill): \"full\" (default) keeps one replica on every eligible node, \"bare\" scales with load between 1 and the eligible set, \"empty\" is bare plus lazy containers so the deployment scales from zero.",
				Optional:    true,
			},
			"strategy": schema.StringAttribute{
				Description: "Placement strategy: \"round-robin\" (default) or \"least-busy\".",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("round-robin"),
			},
			"tags": schema.SetAttribute{
				Description: "Node tags required for placement. All tags must be present on a node (AND'd). Empty means any node is eligible.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"storage": schema.SetAttribute{
				Description: "RCLONE remote names required by this deployment. ${storage.NAME} in compose fields resolves to the mount path.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"compose": schema.StringAttribute{
				Description: "JSON-encoded docker.DockerServiceCreateRequest (services, volumes, networks). Use jsonencode() in HCL. Exclusive with function.",
				Optional:    true,
			},
			"function": schema.StringAttribute{
				Description: "JSON-encoded pro.DeploymentFunction (runtime, source, handlers...) making this a function deployment: the compose is derived from it. Use jsonencode() in HCL. Exclusive with compose.",
				Optional:    true,
			},
			"version": schema.Int64Attribute{
				Description: "Server-assigned spec version, bumped on every create/update.",
				Computed:    true,
			},
		},
	}
}

func (r *deploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.CosmosClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.CosmosClient, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *deploymentResource) buildBody(ctx context.Context, m *deploymentModel) (*cosmossdk.ProDeployment, error) {
	composeStr := m.Compose.ValueString()
	functionStr := m.Function.ValueString()
	if (composeStr == "") == (functionStr == "") {
		return nil, fmt.Errorf("exactly one of compose or function must be set")
	}

	body := &cosmossdk.ProDeployment{
		Name: m.Name.ValueString(),
	}

	if composeStr != "" {
		var compose cosmossdk.DockerDockerServiceCreateRequest
		if err := json.Unmarshal([]byte(composeStr), &compose); err != nil {
			return nil, fmt.Errorf("parsing compose JSON: %w", err)
		}
		body.Compose = &compose
	} else {
		var function cosmossdk.ProDeploymentFunction
		if err := json.Unmarshal([]byte(functionStr), &function); err != nil {
			return nil, fmt.Errorf("parsing function JSON: %w", err)
		}
		body.Function = &function
	}

	if !m.Replicas.IsNull() && !m.Replicas.IsUnknown() {
		body.Replicas = client.IntPtr(int(m.Replicas.ValueInt64()))
	}
	if !m.MinReplicas.IsNull() && !m.MinReplicas.IsUnknown() {
		body.MinReplicas = client.IntPtr(int(m.MinReplicas.ValueInt64()))
	}
	if !m.MaxReplicas.IsNull() && !m.MaxReplicas.IsUnknown() {
		body.MaxReplicas = client.IntPtr(int(m.MaxReplicas.ValueInt64()))
	}
	if !m.ReplicaFill.IsNull() && !m.ReplicaFill.IsUnknown() && m.ReplicaFill.ValueBool() {
		body.ReplicaFill = client.BoolPtr(true)
	}
	if !m.ReplicaFillMode.IsNull() && !m.ReplicaFillMode.IsUnknown() && m.ReplicaFillMode.ValueString() != "" {
		mode := cosmossdk.ProDeploymentReplicaFillMode(m.ReplicaFillMode.ValueString())
		body.ReplicaFillMode = &mode
	}

	if !m.Strategy.IsNull() && !m.Strategy.IsUnknown() && m.Strategy.ValueString() != "" {
		strat := cosmossdk.ProDeploymentStrategy(m.Strategy.ValueString())
		body.Strategy = &strat
	}

	if !m.Tags.IsNull() && !m.Tags.IsUnknown() {
		var tags []string
		if d := m.Tags.ElementsAs(ctx, &tags, false); d.HasError() {
			return nil, fmt.Errorf("parsing tags: %s", d.Errors())
		}
		if len(tags) > 0 {
			body.Tags = &tags
		}
	}

	if !m.Storage.IsNull() && !m.Storage.IsUnknown() {
		var storage []string
		if d := m.Storage.ElementsAs(ctx, &storage, false); d.HasError() {
			return nil, fmt.Errorf("parsing storage: %s", d.Errors())
		}
		if len(storage) > 0 {
			body.Storage = &storage
		}
	}

	return body, nil
}

func (r *deploymentResource) populateState(ctx context.Context, m *deploymentModel, dep *cosmossdk.ProDeployment) error {
	m.Name = types.StringValue(dep.Name)
	m.Version = types.Int64Value(int64(client.IntPtrVal(dep.Version)))

	// The replica-mode fields are only refreshed when the server reports them,
	// so an unset (null) attribute never drifts to a zero value.
	if dep.Replicas != nil && *dep.Replicas > 0 {
		m.Replicas = types.Int64Value(int64(*dep.Replicas))
	} else {
		m.Replicas = types.Int64Null()
	}
	if dep.MinReplicas != nil && *dep.MinReplicas > 0 {
		m.MinReplicas = types.Int64Value(int64(*dep.MinReplicas))
	} else {
		m.MinReplicas = types.Int64Null()
	}
	if dep.MaxReplicas != nil && *dep.MaxReplicas > 0 {
		m.MaxReplicas = types.Int64Value(int64(*dep.MaxReplicas))
	} else {
		m.MaxReplicas = types.Int64Null()
	}
	if dep.ReplicaFill != nil && *dep.ReplicaFill {
		m.ReplicaFill = types.BoolValue(true)
	} else if m.ReplicaFill.IsNull() || m.ReplicaFill.ValueBool() {
		m.ReplicaFill = types.BoolNull()
	}
	if dep.ReplicaFillMode != nil && *dep.ReplicaFillMode != "" {
		m.ReplicaFillMode = types.StringValue(string(*dep.ReplicaFillMode))
	} else {
		m.ReplicaFillMode = types.StringNull()
	}

	if dep.Strategy != nil && *dep.Strategy != "" {
		m.Strategy = types.StringValue(string(*dep.Strategy))
	} else {
		m.Strategy = types.StringValue("round-robin")
	}

	if dep.Tags != nil && len(*dep.Tags) > 0 {
		setVal, d := types.SetValueFrom(ctx, types.StringType, *dep.Tags)
		if d.HasError() {
			return fmt.Errorf("setting tags: %s", d.Errors())
		}
		m.Tags = setVal
	} else {
		m.Tags = types.SetNull(types.StringType)
	}

	if dep.Storage != nil && len(*dep.Storage) > 0 {
		setVal, d := types.SetValueFrom(ctx, types.StringType, *dep.Storage)
		if d.HasError() {
			return fmt.Errorf("setting storage: %s", d.Errors())
		}
		m.Storage = setVal
	} else {
		m.Storage = types.SetNull(types.StringType)
	}

	// Compose and function are intentionally not refreshed from the server. The server
	// roundtrips it through docker.DockerServiceCreateRequest (zero-value
	// fields without omitempty re-emit on response) and further mutates it
	// at deploy time, so the response can't byte-match what the user wrote.
	// Terraform's consistent-result check compares strings, so we keep the
	// user's HCL-authored value as the source of truth in state. Trade-off:
	// out-of-band edits to compose won't be detected by `terraform plan`.

	return nil
}

func (r *deploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := r.buildBody(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment configuration", err.Error())
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationDeployments(ctx, *body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating deployment", err.Error())
		return
	}

	dep, err := client.ParseResponse[cosmossdk.ProDeployment](httpResp)
	if err != nil {
		resp.Diagnostics.AddError("Error parsing deployment create response", err.Error())
		return
	}
	if dep == nil {
		resp.Diagnostics.AddError("Empty deployment create response", "API returned no deployment data")
		return
	}

	if err := r.populateState(ctx, &plan, dep); err != nil {
		resp.Diagnostics.AddError("Error mapping deployment to state", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationDeploymentsName(ctx, name)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error reading deployment", err.Error())
		return
	}

	dep, err := client.ParseResponse[cosmossdk.ProDeployment](httpResp)
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error parsing deployment response", err.Error())
		return
	}
	if dep == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	if err := r.populateState(ctx, &state, dep); err != nil {
		resp.Diagnostics.AddError("Error mapping deployment to state", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *deploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := r.buildBody(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment configuration", err.Error())
		return
	}

	name := plan.Name.ValueString()
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PutApiConstellationDeploymentsName(ctx, name, *body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error updating deployment", err.Error())
		return
	}

	dep, err := client.ParseResponse[cosmossdk.ProDeployment](httpResp)
	if err != nil {
		resp.Diagnostics.AddError("Error parsing deployment update response", err.Error())
		return
	}
	if dep == nil {
		resp.Diagnostics.AddError("Empty deployment update response", "API returned no deployment data")
		return
	}

	if err := r.populateState(ctx, &plan, dep); err != nil {
		resp.Diagnostics.AddError("Error mapping deployment to state", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationDeploymentsName(ctx, name)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting deployment", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		if client.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error deleting deployment", err.Error())
		return
	}
}

func (r *deploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
