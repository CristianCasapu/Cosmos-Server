package resources

import (
	"context"
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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &databaseResource{}
	_ resource.ResourceWithImportState = &databaseResource{}
)

func NewDatabaseResource() resource.Resource {
	return &databaseResource{}
}

type databaseResource struct {
	client *client.CosmosClient
}

type databaseModel struct {
	Name                    types.String `tfsdk:"name"`
	Engine                  types.String `tfsdk:"engine"`
	Image                   types.String `tfsdk:"image"`
	Port                    types.Int64  `tfsdk:"port"`
	RestrictToConstellation types.Bool   `tfsdk:"restrict_to_constellation"`
	RemoveVolumeOnDestroy   types.Bool   `tfsdk:"remove_volume_on_destroy"`
	Route                   types.String `tfsdk:"route"`
	Backup                  *backupModel `tfsdk:"backup"`
	Host                    types.String `tfsdk:"host"`
	HomeNode                types.String `tfsdk:"home_node"`
	User                    types.String `tfsdk:"user"`
	Password                types.String `tfsdk:"password"`
	URL                     types.String `tfsdk:"url"`
}

// databaseConnectionJSON is the payload of GET databases/{name}/connection.
type databaseConnectionJSON struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	HomeNode  string `json:"homeNodeName"`
	User      string `json:"user"`
	Password  string `json:"password"`
	URL       string `json:"url"`
	Databases map[string]struct {
		Database string `json:"database"`
		User     string `json:"user"`
		Password string `json:"password"`
		URL      string `json:"url"`
	} `json:"databases"`
}

// databaseJSON is the part of pro.ManagedDatabaseStatus the provider reads back.
type databaseJSON struct {
	Name                    string `json:"name"`
	Engine                  string `json:"engine"`
	Image                   string `json:"image"`
	Port                    int    `json:"port"`
	RestrictToConstellation bool   `json:"restrictToConstellation"`
}

func (r *databaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (r *databaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Manages a Cosmos managed database instance. Logical databases are managed with cosmos_database_logical. Pro feature.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Unique instance name (3-48 alphanumeric chars). Used as the resource ID.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"engine": schema.StringAttribute{
				Description:   "Database engine. Defaults to postgres.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"image": schema.StringAttribute{
				Description:   "Container image overriding the pinned default.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"port": schema.Int64Attribute{
				Description:   "Proxy listen port. Allocated by the server when unset.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace(), int64planmodifier.UseStateForUnknown()},
			},
			"restrict_to_constellation": schema.BoolAttribute{
				Description: "Only accept connections from the constellation. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"remove_volume_on_destroy": schema.BoolAttribute{
				Description: "Also destroy the data volume when the instance is destroyed. Defaults to false: the data is preserved.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"route":     schema.StringAttribute{Description: routeJSONDescription + " Its RestrictToConstellation wins over restrict_to_constellation when both are set.", Optional: true},
			"backup":    backupSchemaAttribute("Scheduled restic backups of the instance."),
			"host":      schema.StringAttribute{Description: "Address the instance is reachable on.", Computed: true, PlanModifiers: keep},
			"home_node": schema.StringAttribute{Description: "Node hosting the instance.", Computed: true, PlanModifiers: keep},
			"user":      schema.StringAttribute{Description: "Superuser name.", Computed: true, PlanModifiers: keep},
			"password":  schema.StringAttribute{Description: "Superuser password.", Computed: true, Sensitive: true, PlanModifiers: keep},
			"url":       schema.StringAttribute{Description: "Superuser connection URL.", Computed: true, Sensitive: true, PlanModifiers: keep},
		},
	}
}

func (r *databaseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

func readDatabaseConnection(ctx context.Context, c *client.CosmosClient, name string) (*databaseConnectionJSON, error) {
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return c.Raw.GetApiConstellationDatabasesNameConnection(ctx, name)
	})
	if err != nil {
		return nil, err
	}
	return client.ParseResponse[databaseConnectionJSON](httpResp)
}

// refresh reads the record and its connection details; found is false when the instance is gone.
func (r *databaseResource) refresh(ctx context.Context, m *databaseModel) (found bool, err error) {
	name := m.Name.ValueString()
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationDatabasesName(ctx, name)
	})
	if err != nil {
		return false, err
	}
	db, err := client.ParseResponse[databaseJSON](httpResp)
	if err != nil {
		if client.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if db == nil {
		return false, nil
	}
	conn, err := readDatabaseConnection(ctx, r.client, name)
	if err != nil || conn == nil {
		return false, err
	}

	m.Engine = types.StringValue(db.Engine)
	m.Image = types.StringValue(db.Image)
	m.Port = types.Int64Value(int64(db.Port))
	m.RestrictToConstellation = types.BoolValue(db.RestrictToConstellation)
	m.Host = types.StringValue(conn.Host)
	m.HomeNode = types.StringValue(conn.HomeNode)
	m.User = types.StringValue(conn.User)
	m.Password = types.StringValue(conn.Password)
	m.URL = types.StringValue(conn.URL)
	return true, nil
}

func (r *databaseResource) syncBackup(ctx context.Context, name string, plan, state *backupModel) error {
	return syncBackup(plan, state,
		func(body cosmossdk.ProManagedDBBackupRequest) (*http.Response, error) {
			return retryOn503(ctx, func() (*http.Response, error) {
				return r.client.Raw.PutApiConstellationDatabasesNameBackup(ctx, name, body)
			})
		},
		func() (*http.Response, error) {
			return retryOn503(ctx, func() (*http.Response, error) {
				return r.client.Raw.DeleteApiConstellationDatabasesNameBackup(ctx, name)
			})
		})
}

// update pushes the mutable settings (restriction and route).
func (r *databaseResource) update(ctx context.Context, m *databaseModel, diags *diag.Diagnostics) {
	body := cosmossdk.ProManagedDBUpdateRequest{
		RestrictToConstellation: optBool(m.RestrictToConstellation),
		Route:                   parseRouteJSON(m.Route, diags),
	}
	if diags.HasError() {
		return
	}
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PutApiConstellationDatabasesName(ctx, m.Name.ValueString(), body)
	})
	if err != nil {
		diags.AddError("Error updating database", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		diags.AddError("Error updating database", err.Error())
	}
}

func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := cosmossdk.ProManagedDBCreateRequest{
		Name:                    plan.Name.ValueString(),
		Image:                   optString(plan.Image),
		Port:                    optInt(plan.Port),
		RestrictToConstellation: optBool(plan.RestrictToConstellation),
	}
	if engine := optString(plan.Engine); engine != nil {
		e := cosmossdk.ProManagedDBCreateRequestEngine(*engine)
		body.Engine = &e
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationDatabases(ctx, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating database", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error creating database", err.Error())
		return
	}

	// The create request carries neither the route nor the backup.
	if isSet(plan.Route) && plan.Route.ValueString() != "" {
		r.update(ctx, &plan, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if err := r.syncBackup(ctx, plan.Name.ValueString(), plan.Backup, nil); err != nil {
		resp.Diagnostics.AddError("Error configuring database backup", err.Error())
		return
	}

	if found, err := r.refresh(ctx, &plan); err != nil || !found {
		resp.Diagnostics.AddError("Error reading database after create", errString(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found, err := r.refresh(ctx, &state)
	if err != nil {
		resp.Diagnostics.AddError("Error reading database", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	if state.RemoveVolumeOnDestroy.IsNull() {
		state.RemoveVolumeOnDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *databaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.RestrictToConstellation.Equal(state.RestrictToConstellation) || !plan.Route.Equal(state.Route) {
		r.update(ctx, &plan, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if err := r.syncBackup(ctx, plan.Name.ValueString(), plan.Backup, state.Backup); err != nil {
		resp.Diagnostics.AddError("Error configuring database backup", err.Error())
		return
	}

	if found, err := r.refresh(ctx, &plan); err != nil || !found {
		resp.Diagnostics.AddError("Error reading database after update", errString(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := &cosmossdk.DeleteApiConstellationDatabasesNameParams{}
	if state.RemoveVolumeOnDestroy.ValueBool() {
		params.RemoveVolume = client.BoolPtr(true)
	}
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationDatabasesName(ctx, state.Name.ValueString(), params)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting database", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting database", err.Error())
	}
}

func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
