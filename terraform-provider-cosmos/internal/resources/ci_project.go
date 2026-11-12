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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &ciProjectResource{}
	_ resource.ResourceWithImportState = &ciProjectResource{}
)

func NewCIProjectResource() resource.Resource {
	return &ciProjectResource{}
}

type ciProjectResource struct {
	client *client.CosmosClient
}

type ciSecretModel struct {
	Name           types.String `tfsdk:"name"`
	Value          types.String `tfsdk:"value"`
	AvailableToPRs types.Bool   `tfsdk:"available_to_prs"`
}

type ciProjectModel struct {
	Name              types.String    `tfsdk:"name"`
	Description       types.String    `tfsdk:"description"`
	Provider          types.String    `tfsdk:"provider_type"`
	RepoURL           types.String    `tfsdk:"repo_url"`
	APIURL            types.String    `tfsdk:"api_url"`
	Token             types.String    `tfsdk:"token"`
	Username          types.String    `tfsdk:"username"`
	RootDir           types.String    `tfsdk:"root_dir"`
	Branches          types.Set       `tfsdk:"branches"`
	DefaultBranch     types.String    `tfsdk:"default_branch"`
	Build             types.String    `tfsdk:"build"`
	Deploy            types.String    `tfsdk:"deploy"`
	Registry          types.String    `tfsdk:"registry"`
	Image             types.String    `tfsdk:"image"`
	StaticRegistry    types.String    `tfsdk:"static_registry"`
	Secrets           []ciSecretModel `tfsdk:"secrets"`
	TrustPullRequests types.String    `tfsdk:"trust_pull_requests"`
	Tags              types.Set       `tfsdk:"tags"`
	Enabled           types.Bool      `tfsdk:"enabled"`
	WebhookURL        types.String    `tfsdk:"webhook_url"`
	WebhookSecret     types.String    `tfsdk:"webhook_secret"`
	WebhookRegistered types.Bool      `tfsdk:"webhook_registered"`
}

// ciProjectJSON is pro.CIProjectView: the project plus the webhook URL.
type ciProjectJSON struct {
	cosmossdk.ProCIProject
	WebhookURL string `json:"webhookUrl"`
}

func (r *ciProjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ci_project"
}

func (r *ciProjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Manages a Cosmos CI/CD project: a git repository built on the cluster and deployed from its cosmos.json or the deploy rule below. Pro feature.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Unique project name (lowercase letters, digits and dashes, 2-39 chars). Used as the resource ID.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{Optional: true, Description: "Free-form description."},
			"provider_type": schema.StringAttribute{
				Description:   "Git provider: github, gitlab, gitea, bitbucket or git. Detected from repo_url when unset.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
			},
			"repo_url": schema.StringAttribute{
				Description: "HTTPS clone URL of the repository.",
				Required:    true,
			},
			"api_url": schema.StringAttribute{
				Description: "Provider API base for self-hosted GitLab / Gitea / GitHub Enterprise. Derived from repo_url when unset.",
				Optional:    true,
			},
			"token": schema.StringAttribute{
				Description: "Token used to clone and to call the provider API (webhook creation, commit statuses). Write-only on the server.",
				Optional:    true,
				Sensitive:   true,
			},
			"username": schema.StringAttribute{
				Description: "Username paired with the token, for providers that need one.",
				Optional:    true,
			},
			"root_dir": schema.StringAttribute{
				Description: "Sub-folder to build (monorepos).",
				Optional:    true,
			},
			"branches": schema.SetAttribute{
				Description: "Branch filter (globs). Empty builds every branch.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"default_branch": schema.StringAttribute{
				Description:   "Branch built by a manual run. Probed from the repository when unset.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
			},
			"build": schema.StringAttribute{
				Description: "JSON-encoded pro.CIBuildSettings (strategy, dockerfile, publishDir, env, platform, timeoutMinutes). Use jsonencode() in HCL. The repository's cosmos.json wins when it sets the same thing.",
				Optional:    true,
			},
			"deploy": schema.StringAttribute{
				Description: "JSON-encoded pro.CIDeployRule (enabled, type, name, environments, pullRequests, previewTtl, previewHost, template). Use jsonencode() in HCL. The repository's cosmos.json wins when it sets the same thing.",
				Optional:    true,
			},
			"registry": schema.StringAttribute{
				Description: "Cosmos docker registry images are pushed to.",
				Optional:    true,
			},
			"image": schema.StringAttribute{
				Description:   "Image repository name. Defaults to the project name.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
			},
			"static_registry": schema.StringAttribute{
				Description: "Cosmos static registry receiving static-site archives.",
				Optional:    true,
			},
			"secrets": schema.ListNestedAttribute{
				Description: "Secrets exposed to the builds.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{Required: true, Description: "Secret name."},
						"value": schema.StringAttribute{Required: true, Sensitive: true, Description: "Secret value. Write-only on the server."},
						"available_to_prs": schema.BoolAttribute{
							Description: "Also expose the secret to pull-request builds.",
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(false),
						},
					},
				},
			},
			"trust_pull_requests": schema.StringAttribute{
				Description:   "Pull-request trust policy: off, collaborators, nosecrets (default) or approval.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
			},
			"tags": schema.SetAttribute{
				Description: "Node tags selecting the nodes builds may run on. Empty means any node.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether builds are accepted. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"webhook_url":    schema.StringAttribute{Description: "URL the git provider must deliver webhooks to.", Computed: true, PlanModifiers: keep},
			"webhook_secret": schema.StringAttribute{Description: "Secret signing the webhook deliveries, for a manual registration.", Computed: true, Sensitive: true, PlanModifiers: keep},
			"webhook_registered": schema.BoolAttribute{
				Description: "Whether Cosmos registered the webhook on the provider itself.",
				Computed:    true,
			},
		},
	}
}

func (r *ciProjectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

// buildBody renders the full project: the update endpoint replaces every
// editable field, so nothing may be left out.
func (r *ciProjectResource) buildBody(ctx context.Context, m *ciProjectModel, diags *diag.Diagnostics) cosmossdk.ProCIProject {
	body := cosmossdk.ProCIProject{
		Name:        optString(m.Name),
		Description: optString(m.Description),
		Enabled:     client.BoolPtr(!isSet(m.Enabled) || m.Enabled.ValueBool()),
		Tags:        optStrings(ctx, m.Tags, diags),
		Source: &cosmossdk.ProCISource{
			Provider:      optString(m.Provider),
			RepoUrl:       optString(m.RepoURL),
			ApiUrl:        optString(m.APIURL),
			Token:         optString(m.Token),
			Username:      optString(m.Username),
			RootDir:       optString(m.RootDir),
			Branches:      optStrings(ctx, m.Branches, diags),
			DefaultBranch: optString(m.DefaultBranch),
		},
		Registry: &cosmossdk.ProCIRegistryTarget{
			Registry:       optString(m.Registry),
			Image:          optString(m.Image),
			StaticRegistry: optString(m.StaticRegistry),
		},
		Trust: &cosmossdk.ProCITrust{PullRequests: optString(m.TrustPullRequests)},
	}

	if build := m.Build.ValueString(); build != "" {
		var parsed cosmossdk.ProCIBuildSettings
		if err := json.Unmarshal([]byte(build), &parsed); err != nil {
			diags.AddError("Invalid CI project configuration", fmt.Sprintf("parsing build JSON: %s", err))
		}
		body.Build = &parsed
	}
	if deploy := m.Deploy.ValueString(); deploy != "" {
		var parsed cosmossdk.ProCIDeployRule
		if err := json.Unmarshal([]byte(deploy), &parsed); err != nil {
			diags.AddError("Invalid CI project configuration", fmt.Sprintf("parsing deploy JSON: %s", err))
		}
		body.Deploy = &parsed
	}

	if len(m.Secrets) > 0 {
		secrets := make([]cosmossdk.ProCISecret, 0, len(m.Secrets))
		for _, s := range m.Secrets {
			secrets = append(secrets, cosmossdk.ProCISecret{
				Name:           optString(s.Name),
				Value:          optString(s.Value),
				AvailableToPRs: client.BoolPtr(s.AvailableToPRs.ValueBool()),
			})
		}
		body.Secrets = &secrets
	}
	return body
}

// setComputed fills what the server decides, leaving configured attributes as planned.
func (r *ciProjectResource) setComputed(m *ciProjectModel, p *ciProjectJSON) {
	if p.Source != nil {
		if !isSet(m.Provider) {
			m.Provider = types.StringValue(client.StringPtrVal(p.Source.Provider))
		}
		if !isSet(m.DefaultBranch) {
			m.DefaultBranch = types.StringValue(client.StringPtrVal(p.Source.DefaultBranch))
		}
	}
	if !isSet(m.Image) && p.Registry != nil {
		m.Image = types.StringValue(client.StringPtrVal(p.Registry.Image))
	}
	if !isSet(m.TrustPullRequests) && p.Trust != nil {
		m.TrustPullRequests = types.StringValue(client.StringPtrVal(p.Trust.PullRequests))
	}
	// Anything still unknown here was not reported by the server.
	for _, v := range []*types.String{&m.Provider, &m.DefaultBranch, &m.Image, &m.TrustPullRequests} {
		if v.IsUnknown() {
			*v = types.StringValue("")
		}
	}
	m.WebhookURL = types.StringValue(p.WebhookURL)
	if p.Webhook != nil {
		m.WebhookSecret = types.StringValue(client.StringPtrVal(p.Webhook.Secret))
		m.WebhookRegistered = types.BoolValue(client.BoolPtrVal(p.Webhook.Registered))
	} else {
		m.WebhookSecret = types.StringValue("")
		m.WebhookRegistered = types.BoolValue(false)
	}
}

func (r *ciProjectResource) put(ctx context.Context, name string, body cosmossdk.ProCIProject) (*ciProjectJSON, error) {
	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PutApiConstellationCiProjectsName(ctx, name, body)
	})
	if err != nil {
		return nil, err
	}
	return client.ParseResponse[ciProjectJSON](httpResp)
}

func (r *ciProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ciProjectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := r.buildBody(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PostApiConstellationCiProjects(ctx, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating CI project", err.Error())
		return
	}
	project, err := client.ParseResponse[ciProjectJSON](httpResp)
	if err != nil || project == nil {
		resp.Diagnostics.AddError("Error creating CI project", errString(err))
		return
	}

	// A project is always created enabled; disabling it is an update.
	if !plan.Enabled.ValueBool() {
		project, err = r.put(ctx, plan.Name.ValueString(), body)
		if err != nil || project == nil {
			resp.Diagnostics.AddError("Error disabling CI project after create", errString(err))
			return
		}
	}

	r.setComputed(&plan, project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ciProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ciProjectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiConstellationCiProjectsName(ctx, state.Name.ValueString())
	})
	if err != nil {
		resp.Diagnostics.AddError("Error reading CI project", err.Error())
		return
	}
	p, err := client.ParseResponse[ciProjectJSON](httpResp)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error reading CI project", err.Error())
		return
	}
	if p == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	// The token and the secret values are write-only, build and deploy are
	// normalized server-side: those stay as configured. The rest is refreshed.
	state.Description = stringOrNull(client.StringPtrVal(p.Description))
	state.Enabled = types.BoolValue(client.BoolPtrVal(p.Enabled))
	if p.Tags != nil {
		state.Tags = stringSetOrNull(ctx, *p.Tags, &resp.Diagnostics)
	} else {
		state.Tags = types.SetNull(types.StringType)
	}
	if s := p.Source; s != nil {
		state.Provider = types.StringValue(client.StringPtrVal(s.Provider))
		state.RepoURL = types.StringValue(client.StringPtrVal(s.RepoUrl))
		state.APIURL = stringOrNull(client.StringPtrVal(s.ApiUrl))
		state.Username = stringOrNull(client.StringPtrVal(s.Username))
		state.RootDir = stringOrNull(client.StringPtrVal(s.RootDir))
		state.DefaultBranch = types.StringValue(client.StringPtrVal(s.DefaultBranch))
		if s.Branches != nil {
			state.Branches = stringSetOrNull(ctx, *s.Branches, &resp.Diagnostics)
		} else {
			state.Branches = types.SetNull(types.StringType)
		}
	}
	if t := p.Registry; t != nil {
		state.Registry = stringOrNull(client.StringPtrVal(t.Registry))
		state.Image = types.StringValue(client.StringPtrVal(t.Image))
		state.StaticRegistry = stringOrNull(client.StringPtrVal(t.StaticRegistry))
	}
	if p.Trust != nil {
		state.TrustPullRequests = types.StringValue(client.StringPtrVal(p.Trust.PullRequests))
	}
	state.WebhookURL = types.StringValue(p.WebhookURL)
	if p.Webhook != nil {
		state.WebhookSecret = types.StringValue(client.StringPtrVal(p.Webhook.Secret))
		state.WebhookRegistered = types.BoolValue(client.BoolPtrVal(p.Webhook.Registered))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ciProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ciProjectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := r.buildBody(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	project, err := r.put(ctx, plan.Name.ValueString(), body)
	if err != nil || project == nil {
		resp.Diagnostics.AddError("Error updating CI project", errString(err))
		return
	}

	r.setComputed(&plan, project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ciProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ciProjectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiConstellationCiProjectsName(ctx, state.Name.ValueString())
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting CI project", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting CI project", err.Error())
	}
}

func (r *ciProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
