import React, { useEffect, useState } from "react";
import * as API from "../../api";
import PrettyTableView from "../../components/tableView/prettyTableView";
import { PlusOutlined, ReloadOutlined, DeleteOutlined, EditOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Grid,
  IconButton,
  Stack,
  Tooltip,
} from "@mui/material";
import { CosmosCheckbox, CosmosCollapse, CosmosInputText, CosmosSelect } from "./users/formShortcuts";
import { DnsChallengeComp, recordProviders } from "../../utils/dns-challenge-comp";
import { FormikProvider, useFormik } from "formik";
import { LoadingButton } from "@mui/lab";
import ResponsiveButton from "../../components/responseiveButton";
import MainCard from "../../components/MainCard";
import { PERM_CONFIGURATION } from "../../utils/permissions";
import PermissionGuard from "../../components/permissionGuard";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { ConfirmModalDirect } from "../../components/confirmModal";

// one line saying which certificate serves a hostname, reused by the route and hostname views
export const HostCertificateLine = ({ cert }) => {
  const { t } = useTranslation();
  if (!cert) return null;

  const expires = cert.validUntil && dayjs(cert.validUntil).year() > 1
    ? " · " + t('mgmt.config.zones.expires', { date: dayjs(cert.validUntil).format('L') })
    : "";

  const label = {
    zone: t('mgmt.config.zones.certSource.zone', { zone: cert.zone }),
    node: t('mgmt.config.zones.certSource.node'),
    selfsigned: t('mgmt.config.zones.certSource.selfsigned'),
    provided: t('mgmt.config.zones.certSource.provided'),
    none: t('mgmt.config.zones.certSource.none'),
  }[cert.source] || cert.source;

  return (
    <Stack direction="row" spacing={1} alignItems="center">
      <Tooltip title={(cert.hosts || []).join(", ")}>
        <span>{label}{expires}</span>
      </Tooltip>
      {!cert.covered && cert.source !== "none" && (
        <Chip size="small" color="warning" label={t('mgmt.config.zones.notCovered')} />
      )}
    </Stack>
  );
};

// zone, certificate and DynDNS state of one hostname, for the URL pages
export const HostZoneInfo = ({ host }) => {
  const { t } = useTranslation();
  const [zone, setZone] = useState(null);
  const hostname = (host || "").split(":")[0].toLowerCase();

  useEffect(() => {
    let cancelled = false;
    API.zones.list().then((res) => {
      if (!cancelled) {
        setZone((res.data || []).find((z) => z.hosts.includes(hostname)) || false);
      }
    }).catch(() => !cancelled && setZone(false));
    return () => { cancelled = true; };
  }, [hostname]);

  if (!zone) return null;

  return (
    <Stack spacing={1}>
      <Stack direction="row" spacing={1} alignItems="center">
        <Chip size="small" label={zone.Zone} />
        {zone.derived && (
          <Tooltip title={t('mgmt.config.zones.derivedInfo')}>
            <Chip size="small" variant="outlined" label={t('mgmt.config.zones.derivedChip')} />
          </Tooltip>
        )}
        <Chip
          size="small"
          variant="outlined"
          color={zone.ManageRecords ? "success" : "default"}
          label={"DynDNS: " + (zone.ManageRecords ? t('mgmt.config.zones.dyndns.on') : t('mgmt.config.zones.dyndns.off'))}
        />
      </Stack>
      <HostCertificateLine cert={zone.hostCertificates[hostname]} />
    </Stack>
  );
};

const zoneModes = ["LETSENCRYPT", "SELFSIGNED", "PROVIDED", "DISABLED"];

const ZoneDialog = ({ zone, onClose, onSaved }) => {
  const { t } = useTranslation();
  const isNew = !zone.Zone;
  // the key of a provided certificate never comes back from the server
  const hasProvidedCert = !zone.derived && zone.HTTPSCertificateMode === "PROVIDED" && !!zone.TLSCert;

  const formik = useFormik({
    initialValues: {
      Zone: zone.Zone || "",
      HTTPSCertificateMode: zoneModes.includes(zone.HTTPSCertificateMode) ? zone.HTTPSCertificateMode : "LETSENCRYPT",
      TLSCert: zone.derived ? "" : (zone.TLSCert || ""),
      TLSKey: "",
      UseWildcardCertificate: !!zone.UseWildcardCertificate,
      DNSChallengeProvider: zone.DNSChallengeProvider || "",
      DNSChallengeConfig: zone.DNSChallengeConfig || {},
      DNSChallengeResolvers: zone.DNSChallengeResolvers || "",
      DNSChallengePropagationWait: zone.DNSChallengePropagationWait || "",
      DisablePropagationChecks: !!zone.DisablePropagationChecks,
      ManageRecords: isNew ? true : !!zone.ManageRecords,
      WildcardRecord: isNew ? true : !!zone.WildcardRecord,
    },
    validateOnChange: false,
    validate: (values) => {
      const errors = {};
      if (!/^[a-z0-9-]+(\.[a-z0-9-]+)+$/i.test(values.Zone.trim())) {
        errors.Zone = t('mgmt.config.zones.invalidZone');
      }
      return errors;
    },
    onSubmit: async (values, { setErrors, setSubmitting }) => {
      setSubmitting(true);
      const hasProvider = !!values.DNSChallengeProvider;
      const canManage = hasProvider && recordProviders.includes(values.DNSChallengeProvider);
      const provided = values.HTTPSCertificateMode === "PROVIDED";
      try {
        await API.zones.set(values.Zone.trim().toLowerCase(), {
          ...values,
          TLSCert: provided ? values.TLSCert : "",
          TLSKey: provided ? values.TLSKey : "",
          DNSChallengePropagationWait: parseInt(values.DNSChallengePropagationWait, 10) || 0,
          UseWildcardCertificate: hasProvider && values.HTTPSCertificateMode === "LETSENCRYPT" && values.UseWildcardCertificate,
          ManageRecords: canManage && values.ManageRecords,
          WildcardRecord: canManage && values.ManageRecords && values.WildcardRecord,
        });
        setSubmitting(false);
        onSaved();
      } catch (err) {
        setErrors({ submit: err.message });
        setSubmitting(false);
      }
    },
  });

  const hasProvider = !!formik.values.DNSChallengeProvider;
  const canManage = hasProvider && recordProviders.includes(formik.values.DNSChallengeProvider);
  const letsEncrypt = formik.values.HTTPSCertificateMode === "LETSENCRYPT";

  return (
    <Dialog open onClose={onClose} maxWidth="sm" fullWidth>
      <FormikProvider value={formik}>
        {/* rendered inside the settings form: keep the submit from reaching it */}
        <form onSubmit={(e) => { e.stopPropagation(); formik.handleSubmit(e); }}>
          <DialogTitle>{isNew ? t('mgmt.config.zones.newZone') : zone.Zone}</DialogTitle>
          <DialogContent>
            <Grid container spacing={3} style={{ marginTop: "0px" }}>
              {zone.derived && (
                <Grid item xs={12}>
                  <Alert severity="info">{t('mgmt.config.zones.derivedInfo')}</Alert>
                </Grid>
              )}

              {isNew && (
                <CosmosInputText
                  name="Zone"
                  label={t('mgmt.config.zones.zoneLabel')}
                  placeholder="example.com"
                  formik={formik}
                />
              )}

              <CosmosSelect
                name="HTTPSCertificateMode"
                label={t('mgmt.config.security.encryption.httpsCertSelection.httpsCertLabel')}
                formik={formik}
                options={[
                  ["LETSENCRYPT", t('mgmt.config.security.encryption.httpsCertSelection.sslLetsEncryptChoice')],
                  ["SELFSIGNED", t('mgmt.config.security.encryption.httpsCertSelection.sslSelfSignedChoice')],
                  ["PROVIDED", t('mgmt.config.security.encryption.httpsCertSelection.sslProvidedChoice')],
                  ["DISABLED", t('mgmt.config.security.encryption.httpsCertSelection.sslDisabledChoice')],
                ]}
              />

              {formik.values.HTTPSCertificateMode === "PROVIDED" && (
                <>
                  <CosmosInputText
                    multiline
                    name="TLSCert"
                    label={t('newInstall.pubCertInput.pubCertLabel')}
                    placeholder={"-----BEGIN CERTIFICATE-----\nMIIEowIBwIBAA...."}
                    formik={formik}
                  />
                  <CosmosInputText
                    multiline
                    name="TLSKey"
                    label={t('newInstall.privCertInput.privCertLabel')}
                    placeholder={hasProvidedCert ? t('mgmt.config.zones.keepKeyPlaceholder') : "-----BEGIN RSA PRIVATE KEY-----\nQCdYIUkYi...."}
                    formik={formik}
                  />
                </>
              )}

              <DnsChallengeComp
                label={t('mgmt.config.zones.providerLabel')}
                name="DNSChallengeProvider"
                configName="DNSChallengeConfig"
                formik={formik}
              >
                {!hasProvider && letsEncrypt && (
                  <Grid item xs={12}>
                    <Alert severity="info">{t('mgmt.config.zones.noProviderInfo')}</Alert>
                  </Grid>
                )}

                {hasProvider && (letsEncrypt || !canManage) && (
                  <Grid item xs={12}>
                    <Alert severity={canManage ? "success" : "warning"}>
                      {canManage
                        ? t('mgmt.config.zones.providerEnables', { provider: formik.values.DNSChallengeProvider })
                        : t('mgmt.config.zones.recordsUnsupported', { providers: recordProviders.join(", ") })}
                    </Alert>
                  </Grid>
                )}

                {hasProvider && letsEncrypt && (
                  <CosmosCheckbox
                    label={t('mgmt.config.zones.wildcardCertLabel', { zone: formik.values.Zone || "example.com" })}
                    name="UseWildcardCertificate"
                    formik={formik}
                  />
                )}

                {canManage && (
                  <CosmosCheckbox
                    label={t('mgmt.config.zones.manageRecordsLabel')}
                    name="ManageRecords"
                    formik={formik}
                  />
                )}

                {canManage && formik.values.ManageRecords && (
                  <CosmosCheckbox
                    label={t('mgmt.config.zones.wildcardRecordLabel', { zone: formik.values.Zone || "example.com" })}
                    name="WildcardRecord"
                    formik={formik}
                  />
                )}
              </DnsChallengeComp>

              {hasProvider && letsEncrypt && (
                <Grid item xs={12}>
                  <CosmosCollapse title={t('mgmt.config.zones.advanced')}>
                    <Grid container spacing={3}>
                      <CosmosInputText
                        name="DNSChallengeResolvers"
                        label={t('mgmt.config.security.encryption.dnsChallengeAdvanced.resolversLabel')}
                        formik={formik}
                        placeholder="1.1.1.1:53,8.8.8.8:53"
                      />
                      <CosmosCheckbox
                        label={t('mgmt.config.security.encryption.dnsChallengeAdvanced.disablePropagationChecksLabel')}
                        name="DisablePropagationChecks"
                        formik={formik}
                      />
                      <CosmosInputText
                        name="DNSChallengePropagationWait"
                        label={t('mgmt.config.security.encryption.dnsChallengeAdvanced.propagationWaitLabel')}
                        formik={formik}
                        placeholder="30"
                      />
                    </Grid>
                  </CosmosCollapse>
                </Grid>
              )}

              {formik.errors.submit && (
                <Grid item xs={12}>
                  <Alert severity="error">{formik.errors.submit}</Alert>
                </Grid>
              )}
            </Grid>
          </DialogContent>
          <DialogActions>
            <Button onClick={onClose}>{t('global.cancelAction')}</Button>
            <LoadingButton color="primary" variant="contained" type="submit" loading={formik.isSubmitting}>
              {t('global.saveAction')}
            </LoadingButton>
          </DialogActions>
        </form>
      </FormikProvider>
    </Dialog>
  );
};

const ConfigZones = () => {
  const { t } = useTranslation();
  const [zones, setZones] = useState(null);
  const [editZone, setEditZone] = useState(null);
  const [confirmDelete, setConfirmDelete] = useState(null);

  const refresh = async () => {
    try {
      const res = await API.zones.list();
      setZones(res.data || []);
    } catch (err) {
      setZones([]);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const httpsCell = (z) => {
    if (z.HTTPSCertificateMode === "SELFSIGNED") {
      return t('mgmt.config.security.encryption.httpsCertSelection.sslSelfSignedChoice');
    }
    if (z.HTTPSCertificateMode === "PROVIDED") {
      return t('mgmt.config.security.encryption.httpsCertSelection.sslProvidedChoice');
    }
    if (z.HTTPSCertificateMode === "DISABLED") {
      return t('mgmt.config.security.encryption.httpsCertSelection.sslDisabledChoice');
    }
    if (z.HTTPSCertificateMode !== "LETSENCRYPT") {
      return z.HTTPSCertificateMode || "-";
    }
    return z.DNSChallengeProvider
      ? t('mgmt.config.zones.https.dns', { provider: z.DNSChallengeProvider })
      : t('mgmt.config.zones.https.http');
  };

  const certificateCell = (z) => {
    if (z.issuesCertificate) {
      if (!z.certificate) {
        return <Chip size="small" color="warning" label={t('mgmt.config.zones.certPending')} />;
      }
      return (
        <HostCertificateLine cert={{
          source: "zone",
          zone: z.Zone,
          hosts: z.certificate.hosts,
          validUntil: z.certificate.validUntil,
          covered: z.hosts.every((h) => z.hostCertificates[h] && z.hostCertificates[h].source === "zone"),
        }} />
      );
    }
    // no certificate of its own: show what its hostnames are actually served with
    const first = z.hosts.length ? z.hostCertificates[z.hosts[0]] : null;
    return first ? <HostCertificateLine cert={first} /> : "-";
  };

  return (
    <MainCard title={t('mgmt.config.zones.title')}>
      <Stack spacing={2}>
        {editZone && (
          <ZoneDialog
            zone={editZone}
            onClose={() => setEditZone(null)}
            onSaved={() => {
              setEditZone(null);
              refresh();
            }}
          />
        )}
        {confirmDelete && (
          <ConfirmModalDirect
            callback={() => {
              API.zones.remove(confirmDelete).then(() => refresh());
            }}
            content={t('mgmt.config.zones.deleteConfirm', { zone: confirmDelete })}
            onClose={() => setConfirmDelete(null)}
          />
        )}

        <Alert severity="info">{t('mgmt.config.zones.info')}</Alert>

        <Stack direction="row" spacing={2}>
          <PermissionGuard permission={PERM_CONFIGURATION}>
            <ResponsiveButton variant="contained" startIcon={<PlusOutlined />} onClick={() => setEditZone({})}>
              {t('mgmt.config.zones.newZone')}
            </ResponsiveButton>
          </PermissionGuard>
          <ResponsiveButton variant="outlined" startIcon={<ReloadOutlined />} onClick={refresh}>
            {t('global.refresh')}
          </ResponsiveButton>
        </Stack>

        {zones === null ? (
          <center>
            <CircularProgress color="inherit" size={20} />
          </center>
        ) : zones.length === 0 ? (
          <Alert severity="info">{t('mgmt.config.zones.noZones')}</Alert>
        ) : (
          <PrettyTableView
            data={zones}
            getKey={(z) => z.Zone + (z.derived ? ":derived" : "")}
            onRowClick={(z) => setEditZone(z)}
            columns={[
              {
                title: t('mgmt.config.zones.zoneColumn'),
                field: (z) => (
                  <Stack direction="row" alignItems="center" spacing={1}>
                    <span>{z.Zone}</span>
                    {z.derived && (
                      <Tooltip title={t('mgmt.config.zones.derivedInfo')}>
                        <Chip size="small" label={t('mgmt.config.zones.derivedChip')} />
                      </Tooltip>
                    )}
                  </Stack>
                ),
              },
              {
                title: "HTTPS",
                field: httpsCell,
              },
              {
                title: t('mgmt.config.zones.certificateColumn'),
                field: certificateCell,
              },
              {
                title: "DynDNS",
                field: (z) => {
                  if (!z.ManageRecords) {
                    return <Chip size="small" label={t('mgmt.config.zones.dyndns.off')} />;
                  }
                  if (z.records && z.records.lastError) {
                    return (
                      <Tooltip title={z.records.lastError}>
                        <Chip size="small" color="error" label={t('mgmt.config.zones.dyndns.error')} />
                      </Tooltip>
                    );
                  }
                  const names = z.records ? Object.keys(z.records.records || {}) : [];
                  const detail = z.records
                    ? names.map((n) => n + " → " + z.records.records[n].join(", ")).join(" · ")
                      + " · " + t('mgmt.config.zones.dyndns.synced', { date: dayjs(z.records.lastSync).format('L, LT') })
                    : t('mgmt.config.zones.dyndns.remote');
                  return (
                    <Tooltip title={detail}>
                      <Chip size="small" color="success" label={z.WildcardRecord ? t('mgmt.config.zones.dyndns.wildcard') : t('mgmt.config.zones.dyndns.on')} />
                    </Tooltip>
                  );
                },
              },
              {
                title: t('mgmt.config.zones.hostsColumn'),
                field: (z) => (
                  <Tooltip title={z.hosts.join(", ")}>
                    <span>{z.hosts.length}</span>
                  </Tooltip>
                ),
              },
              {
                title: "",
                clickable: true,
                field: (z) => (
                  <PermissionGuard permission={PERM_CONFIGURATION}>
                    <Stack direction="row" spacing={1} justifyContent="flex-end">
                      <IconButton size="small" onClick={() => setEditZone(z)}>
                        <EditOutlined />
                      </IconButton>
                      {!z.derived && (
                        <IconButton size="small" color="error" onClick={() => setConfirmDelete(z.Zone)}>
                          <DeleteOutlined />
                        </IconButton>
                      )}
                    </Stack>
                  </PermissionGuard>
                ),
              },
            ]}
          />
        )}
      </Stack>
    </MainCard>
  );
};

export default ConfigZones;
