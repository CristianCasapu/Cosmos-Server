package resources

import (
	"context"
	"net/http"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &databaseLogicalResource{}

func NewDatabaseLogicalResource() resource.Resource {
	return &databaseLogicalResource{}
}

type databaseLogicalResource struct {
	client *client.CosmosClient
}

type databaseLogicalModel struct {
	Instance types.String `tfsdk:"instance"`
	Database types.String `tfsdk:"database"`
	Role     types.String `tfsdk:"role"`
	Password types.String `tfsdk:"password"`
	URL      types.String `tfsdk:"url"`
}

func (r *databaseLogicalResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database_logical"
}

func (r *databaseLogicalResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a logical database (and its owning role) inside a cosmos_database instance. Pro feature.",
		Attributes: map[string]schema.Attribute{
			"instance": schema.StringAttribute{
				Description:   "Name of the cosmos_database instance.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"database": schema.StringAttribute{
				Description:   "Logical database name.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Description:   "Owning role. Defaults to the database name.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"password": schema.StringAttribute{
				Description: "Password of the role.",
				Computed:    true,
				Sensitive:   true,
			},
			"url": schema.StringAttribute{
				Description: "Connection URL of the logical database.",
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *databaseLogicalResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

// refresh reads the credentials back; found is false when the instance or the database is gone.
func (r *databaseLogicalResource) refresh(ctx context.Context, m *databaseLogicalModel) (found bool, err error) {
	conn, err := readDatabaseConnection(ctx, r.client, m.Instance.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if conn == nil {
		return false, nil
	}
	logical, ok := conn.Databases[m.Database.ValueString()]
	if !ok {
		return false, nil
	}
	m.Role = types.StringValue(logical.User)
	m.Password = types.StringValue(logical.Password)
	m.URL = types.StringValue(logical.URL)
	return true, nil
}

func (r *databaseLogicalResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan databaseLogicalModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := cosmossdk.ProManagedDBLogicalRequest{
		Database: plan.Database.ValueString(),
		Role:     optString(plan.Role),
	}
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationDatabasesNameDatabases(ctx, plan.Instance.ValueString(), body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating logical database", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error creating logical database", err.Error())
		return
	}

	if found, err := r.refresh(ctx, &plan); err != nil || !found {
		resp.Diagnostics.AddError("Error reading logical database after create", errString(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseLogicalResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state databaseLogicalModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found, err := r.refresh(ctx, &state)
	if err != nil {
		resp.Diagnostics.AddError("Error reading logical database", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *databaseLogicalResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Logical databases are immutable", "Every attribute of a logical database requires replacement.")
}

func (r *databaseLogicalResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state databaseLogicalModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationDatabasesNameDatabasesDatabase(ctx, state.Instance.ValueString(), state.Database.ValueString())
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting logical database", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting logical database", err.Error())
	}
}
