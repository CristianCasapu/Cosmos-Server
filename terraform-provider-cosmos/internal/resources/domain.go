package resources

import (
	"context"
	"net/http"
	"strings"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &domainResource{}
	_ resource.ResourceWithImportState = &domainResource{}
)

func NewDomainResource() resource.Resource {
	return &domainResource{}
}

type domainResource struct {
	client *client.CosmosClient
}

type domainModel struct {
	Domain                      types.String `tfsdk:"domain"`
	HTTPSCertificateMode        types.String `tfsdk:"https_certificate_mode"`
	TLSCert                     types.String `tfsdk:"tls_cert"`
	TLSKey                      types.String `tfsdk:"tls_key"`
	UseWildcardCertificate      types.Bool   `tfsdk:"use_wildcard_certificate"`
	DNSChallengeProvider        types.String `tfsdk:"dns_challenge_provider"`
	DNSChallengeConfig          types.Map    `tfsdk:"dns_challenge_config"`
	DNSChallengeResolvers       types.String `tfsdk:"dns_challenge_resolvers"`
	DNSChallengePropagationWait types.Int64  `tfsdk:"dns_challenge_propagation_wait"`
	DisablePropagationChecks    types.Bool   `tfsdk:"disable_propagation_checks"`
	ManageRecords               types.Bool   `tfsdk:"manage_records"`
	WildcardRecord              types.Bool   `tfsdk:"wildcard_record"`
}

// domainJSON is a domain as GET /api/zones lists it
type domainJSON struct {
	Zone                        string
	HTTPSCertificateMode        string
	UseWildcardCertificate      bool
	DNSChallengeProvider        string
	DNSChallengeResolvers       string
	DNSChallengePropagationWait int
	DisablePropagationChecks    bool
	ManageRecords               bool
	WildcardRecord              bool
	Derived                     bool `json:"derived"`
}

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A domain with its HTTPS and DynDNS setup. Every hostname under the domain uses it, unless a longer domain matches. Shared by every server of a Constellation.",
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Description:   "The domain name, e.g. example.com.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"https_certificate_mode": schema.StringAttribute{
				Description: "How the hostnames of the domain are served: LETSENCRYPT (default), SELFSIGNED, PROVIDED (tls_cert and tls_key) or DISABLED (plain HTTP).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("LETSENCRYPT"),
			},
			"tls_cert": schema.StringAttribute{
				Description: "PEM-encoded certificate when https_certificate_mode = \"PROVIDED\".",
				Optional:    true,
			},
			"tls_key": schema.StringAttribute{
				Description: "PEM-encoded private key when https_certificate_mode = \"PROVIDED\".",
				Optional:    true,
				Sensitive:   true,
			},
			"use_wildcard_certificate": schema.BoolAttribute{
				Description: "Issue one certificate for the domain and *.domain through the DNS challenge. Needs a dns_challenge_provider.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"dns_challenge_provider": schema.StringAttribute{
				Description: "DNS provider of the domain (e.g. cloudflare), used for the DNS challenge and for DynDNS.",
				Optional:    true,
			},
			"dns_challenge_config": schema.MapAttribute{
				Description: "Credentials of the DNS provider, by environment variable name (e.g. CF_DNS_API_TOKEN).",
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"dns_challenge_resolvers": schema.StringAttribute{
				Description: "Comma-separated DNS resolvers to use for the DNS challenge.",
				Optional:    true,
			},
			"dns_challenge_propagation_wait": schema.Int64Attribute{
				Description: "Seconds to wait for the DNS challenge record to propagate.",
				Optional:    true,
			},
			"disable_propagation_checks": schema.BoolAttribute{
				Description: "Skip the propagation checks of the DNS challenge.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"manage_records": schema.BoolAttribute{
				Description: "DynDNS: keep the DNS records of the hostnames under the domain pointed at the servers serving them. Needs a dns_challenge_provider that can manage records.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"wildcard_record": schema.BoolAttribute{
				Description: "DynDNS: also point *.domain at the server or cluster. Needs manage_records.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
		},
	}
}

func (r *domainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req, resp); c != nil {
		r.client = c
	}
}

func domainName(m *domainModel) string {
	return strings.ToLower(strings.TrimSpace(m.Domain.ValueString()))
}

func (r *domainResource) put(ctx context.Context, plan *domainModel, resp interface{ AddError(string, string) }, action string) bool {
	body := cosmossdk.UtilsDNSZoneConfig{
		Zone:                        domainName(plan),
		HttpscertificateMode:        optString(plan.HTTPSCertificateMode),
		TLSCert:                     optString(plan.TLSCert),
		TLSKey:                      optString(plan.TLSKey),
		UseWildcardCertificate:      optBool(plan.UseWildcardCertificate),
		DnschallengeProvider:        optString(plan.DNSChallengeProvider),
		DnschallengeResolvers:       optString(plan.DNSChallengeResolvers),
		DnschallengePropagationWait: optInt(plan.DNSChallengePropagationWait),
		DisablePropagationChecks:    optBool(plan.DisablePropagationChecks),
		ManageRecords:               optBool(plan.ManageRecords),
		WildcardRecord:              optBool(plan.WildcardRecord),
	}
	if isSet(plan.DNSChallengeConfig) {
		config := map[string]string{}
		if d := plan.DNSChallengeConfig.ElementsAs(ctx, &config, false); d.HasError() {
			resp.AddError("Error "+action+" domain", "invalid dns_challenge_config")
			return false
		}
		body.DNSChallengeConfig = &config
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.PutApiZonesZone(ctx, body.Zone, body)
	})
	if err != nil {
		resp.AddError("Error "+action+" domain", err.Error())
		return false
	}
	if err := client.CheckResponse(httpResp); err != nil {
		resp.AddError("Error "+action+" domain", body.Zone+": "+err.Error())
		return false
	}
	return true
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, &plan, &resp.Diagnostics, "creating") {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.GetApiZones(ctx)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error reading domain", err.Error())
		return
	}
	domains, err := client.ParseResponse[[]domainJSON](httpResp)
	if err != nil {
		resp.Diagnostics.AddError("Error reading domain", err.Error())
		return
	}

	var found *domainJSON
	if domains != nil {
		for i, d := range *domains {
			// an automatic domain is not stored: it only stands for a hostname in use
			if !d.Derived && strings.EqualFold(d.Zone, domainName(&state)) {
				found = &(*domains)[i]
			}
		}
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	// the credentials and the provided certificate are write-only: kept from the state
	state.Domain = types.StringValue(found.Zone)
	state.HTTPSCertificateMode = types.StringValue(found.HTTPSCertificateMode)
	state.UseWildcardCertificate = types.BoolValue(found.UseWildcardCertificate)
	state.DNSChallengeProvider = stringOrNull(found.DNSChallengeProvider)
	state.DNSChallengeResolvers = stringOrNull(found.DNSChallengeResolvers)
	if found.DNSChallengePropagationWait != 0 || !state.DNSChallengePropagationWait.IsNull() {
		state.DNSChallengePropagationWait = types.Int64Value(int64(found.DNSChallengePropagationWait))
	}
	state.DisablePropagationChecks = types.BoolValue(found.DisablePropagationChecks)
	state.ManageRecords = types.BoolValue(found.ManageRecords)
	state.WildcardRecord = types.BoolValue(found.WildcardRecord)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, &plan, &resp.Diagnostics, "updating") {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	httpResp, err := retryOn503(ctx, func() (*http.Response, error) {
		return r.client.Raw.DeleteApiZonesZone(ctx, domainName(&state))
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting domain", err.Error())
		return
	}
	if err := client.CheckResponse(httpResp); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting domain", domainName(&state)+": "+err.Error())
	}
}

func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("domain"), req, resp)
}
