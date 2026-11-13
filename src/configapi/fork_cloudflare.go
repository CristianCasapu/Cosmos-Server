package configapi

// Fork addition: what a full settings save (PUT /api/config) may not change
// about Cloudflare.
//
// The settings page sends back the configuration it read. Two things in it
// are not the page's to write:
//
//   - the API token of a Cloudflare domain, which the page only ever got as
//     the mask (utils.MaskCloudflareSecrets): the stored token is put back;
//   - HTTPConfig.Cloudflare, which holds the admin's choice per application,
//     whether Cloudflare's addresses are trusted and the address ranges
//     themselves. It changes only through the routes of src/cloudflare, which
//     check every value; a page loaded before a switch was flipped must not
//     undo it, and the ranges must never come from a client.

import "github.com/azukaar/cosmos-server/src/utils"

// keepCloudflareState is the hook of ConfigApiSet. stored is the
// configuration on disk.
func keepCloudflareState(request *utils.Config, stored utils.Config) {
	utils.RestoreCloudflareSecrets(request.HTTPConfig.DNSZones, stored.HTTPConfig.DNSZones)
	request.HTTPConfig.Cloudflare = stored.HTTPConfig.Cloudflare
}
