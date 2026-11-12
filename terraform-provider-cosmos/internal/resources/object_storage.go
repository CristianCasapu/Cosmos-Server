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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &objectStorageResource{}
	_ resource.ResourceWithImportState = &objectStorageResource{}
)

func NewObjectStorageResource() resource.Resource {
	return &objectStorageResource{}
}

type objectStorageResource struct {
	client *client.CosmosClient
}

type objectStorageModel struct {
	Name                    types.String `tfsdk:"name"`
	Tags                    types.Set    `tfsdk:"tags"`
	Image                   types.String `tfsdk:"image"`
	FilerReplicas           types.Int64  `tfsdk:"filer_replicas"`
	DefaultReplication      types.String `tfsdk:"default_replication"`
	IndexMode               types.String `tfsdk:"index_mode"`
	VolumeSizeLimitMB       types.Int64  `tfsdk:"volume_size_limit_mb"`
	MinFreeSpace            types.String `tfsdk:"min_free_space"`
	MaxStorageGBPerNode     types.Int64  `tfsdk:"max_storage_gb_per_node"`
	RestrictToConstellation types.Bool   `tfsdk:"restrict_to_constellation"`
	PurgeDataOnDestroy      types.Bool   `tfsdk:"purge_data_on_destroy"`
	KeepFilerDBOnDestroy    types.Bool   `tfsdk:"keep_filer_db_on_destroy"`
	Jobs                    types.String `tfsdk:"jobs"`
	Route                   types.String `tfsdk:"route"`
	Backup                  *backupModel `tfsdk:"backup"`
	S3Port                  types.Int64  `tfsdk:"s3_port"`
	S3AccessKey             types.String `tfsdk:"s3_access_key"`
	S3SecretKey             types.String `tfsdk:"s3_secret_key"`
	S3Endpoints             types.List   `tfsdk:"s3_endpoints"`
	Status                  types.String `tfsdk:"status"`
}

// objectStorageStatusJSON is the payload of GET seaweedfs/{name}/status.
type objectStorageStatusJSON struct {
	Instance struct {
		Name                    string                 `json:"name"`
		Tags                    []string               `json:"tags"`
		Image                   string                 `json:"image"`
		FilerReplicas           int                    `json:"filerReplicas"`
		DefaultReplication      string                 `json:"defaultReplication"`
		IndexMode               string                 `json:"indexMode"`
		VolumeSizeLimitMB       int                    `json:"volumeSizeLimitMB"`
		MinFreeSpace            string                 `json:"minFreeSpace"`
		MaxStorageGBPerNode     int                    `json:"maxStorageGBPerNode"`
		RestrictToConstellation bool                   `json:"restrictToConstellation"`
		S3Port                  int                    `json:"s3Port"`
		PendingUpgradeImage     string                 `json:"pendingUpgradeImage"`
		Jobs                    map[string]interface{} `json:"jobs"`
		S3AccessKey             string                 `json:"s3AccessKey"`
		S3SecretKey             string                 `json:"s3SecretKey"`
		Status                  string                 `json:"status"`
	} `json:"instance"`
	Endpoints []string `json:"endpoints"`
}

func (r *objectStorageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage"
}

func (r *objectStorageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()}
	replaceInt := []planmodifier.Int64{int64planmodifier.RequiresReplace(), int64planmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Manages a Cosmos managed object storage (S3-compatible SeaweedFS) instance. Needs at least 3 constellation managers online. The cluster layout (tags, replication, index mode, volume size...) is fixed at creation; the image, storage cap, restriction, route, jobs and backup change in place. Pro feature.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Unique instance name (3-27 alphanumeric chars). Used as the resource ID.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tags": schema.SetAttribute{
				Description:   "Node tags selecting the nodes that store data. At least one is required.",
				Required:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
			},
			"image":                   schema.StringAttribute{Description: "Container image overriding the pinned default. Changed in place with a rolling upgrade: the masters one at a time, then the volume and filer servers.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"filer_replicas":          schema.Int64Attribute{Description: "Number of filer + S3 gateway replicas (1-16).", Optional: true, Computed: true, PlanModifiers: replaceInt},
			"default_replication":     schema.StringAttribute{Description: "SeaweedFS replication code (3 digits, e.g. \"001\").", Optional: true, Computed: true, PlanModifiers: replaceString},
			"index_mode":              schema.StringAttribute{Description: "Volume index mode: leveldb or memory.", Optional: true, Computed: true, PlanModifiers: replaceString},
			"volume_size_limit_mb":    schema.Int64Attribute{Description: "Maximum size of one volume, in MB.", Optional: true, Computed: true, PlanModifiers: replaceInt},
			"min_free_space":          schema.StringAttribute{Description: "Free space to keep on every data node (e.g. \"10GiB\" or \"5\" for percent).", Optional: true, Computed: true, PlanModifiers: replaceString},
			"max_storage_gb_per_node": schema.Int64Attribute{Description: "Storage cap per node, in GB. 0 means unlimited. Changed in place: the volume servers are rolled one node at a time.", Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"restrict_to_constellation": schema.BoolAttribute{
				Description: "Only accept S3 connections from the constellation. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"purge_data_on_destroy": schema.BoolAttribute{
				Description: "Also remove the data volumes on every node when the instance is destroyed. Defaults to false: the data is preserved.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"keep_filer_db_on_destroy": schema.BoolAttribute{
				Description: "Keep the auto-provisioned filer database when the instance is destroyed.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"jobs": schema.StringAttribute{
				Description: "JSON-encoded pro.SwfsJobsConfig overrides for the maintenance jobs (vacuumEnabled, vacuumCrontab, garbageThreshold, ecEnabled, ecCrontab, scrubEnabled, scrubCrontab, diskAlertPercent...). Use jsonencode() in HCL. Merged over the instance's current configuration, so only the keys you set change. Not refreshed from the server.",
				Optional:    true,
			},
			"route":         schema.StringAttribute{Description: routeJSONDescription, Optional: true},
			"backup":        backupSchemaAttribute("Scheduled restic backups of the instance's metadata (the filer database)."),
			"s3_port":       schema.Int64Attribute{Description: "Port of the S3 endpoint.", Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"s3_access_key": schema.StringAttribute{Description: "S3 access key.", Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"s3_secret_key": schema.StringAttribute{Description: "S3 secret key.", Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"s3_endpoints":  schema.ListAttribute{Description: "S3 endpoint URLs, the cluster hostname first.", Computed: true, ElementType: types.StringType},
			"status":        schema.StringAttribute{Description: "Instance status reported by the server.", Computed: true},
		},
	}
}

func (r *objectStorageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

func (r *objectStorageResource) load(ctx context.Context, name string) (*objectStorageStatusJSON, error) {
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationSeaweedfsNameStatus(ctx, name)
	})
	if err != nil {
		return nil, err
	}
	st, err := client.ParseResponse[objectStorageStatusJSON](httpResp)
	if err != nil {
		if client.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return st, nil
}

// apply copies the server's view into the model. The settings fixed at
// creation are only filled where the plan left them unknown, so a create keeps
// the planned values; Read passes overwrite to pick up the real ones.
func (r *objectStorageResource) apply(ctx context.Context, m *objectStorageModel, st *objectStorageStatusJSON, overwrite bool, diags *diag.Diagnostics) {
	in := st.Instance
	if overwrite || !isSet(m.Image) {
		// A rolling upgrade only lands on the record once the masters are done.
		if in.PendingUpgradeImage != "" {
			m.Image = types.StringValue(in.PendingUpgradeImage)
		} else {
			m.Image = types.StringValue(in.Image)
		}
	}
	if overwrite || !isSet(m.FilerReplicas) {
		m.FilerReplicas = types.Int64Value(int64(in.FilerReplicas))
	}
	if overwrite || !isSet(m.DefaultReplication) {
		m.DefaultReplication = types.StringValue(in.DefaultReplication)
	}
	if overwrite || !isSet(m.IndexMode) {
		m.IndexMode = types.StringValue(in.IndexMode)
	}
	if overwrite || !isSet(m.VolumeSizeLimitMB) {
		m.VolumeSizeLimitMB = types.Int64Value(int64(in.VolumeSizeLimitMB))
	}
	if overwrite || !isSet(m.MinFreeSpace) {
		m.MinFreeSpace = types.StringValue(in.MinFreeSpace)
	}
	if overwrite || !isSet(m.MaxStorageGBPerNode) {
		m.MaxStorageGBPerNode = types.Int64Value(int64(in.MaxStorageGBPerNode))
	}
	if overwrite {
		m.Tags = stringSetOrNull(ctx, in.Tags, diags)
		m.RestrictToConstellation = types.BoolValue(in.RestrictToConstellation)
	}
	m.S3Port = types.Int64Value(int64(in.S3Port))
	m.S3AccessKey = types.StringValue(in.S3AccessKey)
	m.S3SecretKey = types.StringValue(in.S3SecretKey)
	m.Status = types.StringValue(in.Status)
	endpoints, d := types.ListValueFrom(ctx, types.StringType, append([]string{}, st.Endpoints...))
	diags.Append(d...)
	m.S3Endpoints = endpoints
}

// call runs one settings request and checks its envelope.
func (r *objectStorageResource) call(ctx context.Context, do func() (*http.Response, error)) error {
	httpResp, err := retryOn503(ctx, do)
	if err != nil {
		return err
	}
	return client.CheckResponse(httpResp)
}

// syncSettings applies what can change in place. state is nil on create,
// where the create request already carried the image, cap and restriction.
func (r *objectStorageResource) syncSettings(ctx context.Context, plan, state *objectStorageModel, diags *diag.Diagnostics) {
	name := plan.Name.ValueString()
	fail := func(what string, err error) { diags.AddError("Error updating object storage "+what, err.Error()) }

	if state != nil {
		if !plan.RestrictToConstellation.Equal(state.RestrictToConstellation) {
			body := cosmossdk.ProSeaweedFSRestrictRequest{RestrictToConstellation: client.BoolPtr(plan.RestrictToConstellation.ValueBool())}
			if err := r.call(ctx, func() (*http.Response, error) {
				return r.client.Raw.PostApiConstellationSeaweedfsNameRestrict(ctx, name, body)
			}); err != nil {
				fail("restriction", err)
				return
			}
		}
		if isSet(plan.MaxStorageGBPerNode) && !plan.MaxStorageGBPerNode.Equal(state.MaxStorageGBPerNode) {
			body := cosmossdk.ProSeaweedFSStorageRequest{MaxStorageGBPerNode: optInt(plan.MaxStorageGBPerNode)}
			if err := r.call(ctx, func() (*http.Response, error) {
				return r.client.Raw.PutApiConstellationSeaweedfsNameStorage(ctx, name, body)
			}); err != nil {
				fail("storage cap", err)
				return
			}
		}
		if image := optString(plan.Image); image != nil && !plan.Image.Equal(state.Image) {
			body := cosmossdk.ProSeaweedFSUpgradeRequest{Image: image}
			if err := r.call(ctx, func() (*http.Response, error) {
				return r.client.Raw.PostApiConstellationSeaweedfsNameUpgrade(ctx, name, body)
			}); err != nil {
				fail("image", err)
				return
			}
		}
	}

	if jobs := plan.Jobs.ValueString(); jobs != "" && (state == nil || !plan.Jobs.Equal(state.Jobs)) {
		// The endpoint replaces the whole configuration: start from the current one.
		st, err := r.load(ctx, name)
		if err != nil || st == nil {
			fail("jobs", fmt.Errorf("reading the current configuration: %s", errString(err)))
			return
		}
		merged := st.Instance.Jobs
		if merged == nil {
			merged = map[string]interface{}{}
		}
		overrides := map[string]interface{}{}
		if err := json.Unmarshal([]byte(jobs), &overrides); err != nil {
			diags.AddError("Invalid object storage configuration", fmt.Sprintf("parsing jobs JSON: %s", err))
			return
		}
		for k, v := range overrides {
			merged[k] = v
		}
		var body cosmossdk.ProSwfsJobsConfig
		raw, _ := json.Marshal(merged)
		if err := json.Unmarshal(raw, &body); err != nil {
			diags.AddError("Invalid object storage configuration", fmt.Sprintf("jobs JSON does not match pro.SwfsJobsConfig: %s", err))
			return
		}
		if err := r.call(ctx, func() (*http.Response, error) {
			return r.client.Raw.PutApiConstellationSeaweedfsNameJobs(ctx, name, body)
		}); err != nil {
			fail("jobs", err)
			return
		}
	}

	if state == nil || !plan.Route.Equal(state.Route) {
		if route := parseRouteJSON(plan.Route, diags); route != nil {
			body := cosmossdk.ProSeaweedFSRouteRequest{Route: route}
			if err := r.call(ctx, func() (*http.Response, error) {
				return r.client.Raw.PutApiConstellationSeaweedfsNameRoute(ctx, name, body)
			}); err != nil {
				fail("route", err)
				return
			}
		}
		if diags.HasError() {
			return
		}
	}

	var stateBackup *backupModel
	if state != nil {
		stateBackup = state.Backup
	}
	if err := syncBackup(plan.Backup, stateBackup,
		func(body cosmossdk.ProManagedDBBackupRequest) (*http.Response, error) {
			return retryOn503(ctx, func() (*http.Response, error) {
				return r.client.Raw.PutApiConstellationSeaweedfsNameBackup(ctx, name, body)
			})
		},
		func() (*http.Response, error) {
			return retryOn503(ctx, func() (*http.Response, error) {
				return r.client.Raw.DeleteApiConstellationSeaweedfsNameBackup(ctx, name)
			})
		}); err != nil {
		fail("backup", err)
	}
}

func (r *objectStorageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectStorageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := cosmossdk.ProSeaweedFSCreateRequest{
		Name:                    optString(plan.Name),
		Tags:                    optStrings(ctx, plan.Tags, &resp.Diagnostics),
		Image:                   optString(plan.Image),
		FilerReplicas:           optInt(plan.FilerReplicas),
		DefaultReplication:      optString(plan.DefaultReplication),
		IndexMode:               optString(plan.IndexMode),
		VolumeSizeLimitMB:       optInt(plan.VolumeSizeLimitMB),
		MinFreeSpace:            optString(plan.MinFreeSpace),
		MaxStorageGBPerNode:     optInt(plan.MaxStorageGBPerNode),
		RestrictToConstellation: optBool(plan.RestrictToConstellation),
	}
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationSeaweedfs(ctx, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating object storage", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error creating object storage", err.Error())
		return
	}

	r.syncSettings(ctx, &plan, nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	st, err := r.load(ctx, plan.Name.ValueString())
	if err != nil || st == nil {
		resp.Diagnostics.AddError("Error reading object storage after create", errString(err))
		return
	}
	r.apply(ctx, &plan, st, false, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectStorageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectStorageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	st, err := r.load(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading object storage", err.Error())
		return
	}
	if st == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	r.apply(ctx, &state, st, true, &resp.Diagnostics)
	if state.PurgeDataOnDestroy.IsNull() {
		state.PurgeDataOnDestroy = types.BoolValue(false)
	}
	if state.KeepFilerDBOnDestroy.IsNull() {
		state.KeepFilerDBOnDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only ever sees the in-place settings and the destroy flags change:
// the cluster layout requires replacement.
func (r *objectStorageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state objectStorageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.syncSettings(ctx, &plan, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	st, err := r.load(ctx, plan.Name.ValueString())
	if err != nil || st == nil {
		resp.Diagnostics.AddError("Error reading object storage after update", errString(err))
		return
	}
	r.apply(ctx, &plan, st, false, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectStorageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectStorageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := &cosmossdk.DeleteApiConstellationSeaweedfsNameParams{}
	if state.PurgeDataOnDestroy.ValueBool() {
		params.PurgeData = client.BoolPtr(true)
	}
	if state.KeepFilerDBOnDestroy.ValueBool() {
		params.KeepFilerDB = client.BoolPtr(true)
	}
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationSeaweedfsName(ctx, state.Name.ValueString(), params)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting object storage", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting object storage", err.Error())
	}
}

func (r *objectStorageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
