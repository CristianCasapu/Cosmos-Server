// Fork addition: the Domains dialog filled by hand (configZones.jsx), guided.
// The certificate mode is one choice with a sentence per mode, proposed from
// the name (a name for use at home or an address: self-signed; a public name:
// Let's Encrypt) as long as the admin has not picked one; for a new public
// name the server is asked for its name servers when the name is left
// (POST /api/certificates/domain, the lookup the guided "Add another domain"
// uses), and the DNS provider they belong to is proposed with the wildcard.
// Nothing chosen is ever replaced: a proposal that differs waits with a button.
import * as React from 'react';
import { FormHelperText, Grid } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import { ChoiceRow } from '../../components/formHelpers';
import ProposalBox from '../../components/proposalBox';
import { MODES, modeHint, proposeMode, cleanZone, isZoneName, isLocalZone } from '../../utils/zoneMode';
import { proposalFor } from '../../utils/certificates';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

const LABELS = {
  LETSENCRYPT: ['mgmt.config.zones.fork.modeLetsEncrypt', 'Let\'s Encrypt'],
  SELFSIGNED: ['mgmt.config.zones.fork.modeSelfSigned', 'Self-signed'],
  PROVIDED: ['mgmt.config.zones.fork.modeProvided', 'My own certificate'],
  DISABLED: ['mgmt.config.zones.fork.modeDisabled', 'HTTP only'],
};

export const ForkZoneMode = ({ formik, isNew }) => {
  const { t } = useTranslation();
  const zone = cleanZone(formik.values.Zone);
  // the mode the last proposal put there; a mode the admin picks differs from it
  const [mark, setMark] = React.useState(isNew ? formik.values.HTTPSCertificateMode : null);
  const proposal = isNew ? proposeMode(zone) : null;

  React.useEffect(() => {
    if (!proposal || mark === null) return;
    if (formik.values.HTTPSCertificateMode === mark && proposal.mode !== mark) {
      formik.setFieldValue('HTTPSCertificateMode', proposal.mode);
      setMark(proposal.mode);
    }
  }, [proposal && proposal.mode]);

  const held = proposal && formik.values.HTTPSCertificateMode !== proposal.mode;
  return <>
    <ChoiceRow
      name="fork-zone-mode"
      label={t('mgmt.config.security.encryption.httpsCertSelection.httpsCertLabel')}
      value={formik.values.HTTPSCertificateMode}
      onChange={(value) => formik.setFieldValue('HTTPSCertificateMode', value)}
      options={MODES.map((mode) => ({ value: mode, label: t(LABELS[mode][0], LABELS[mode][1]), hint: tr(t, modeHint(mode)) }))}
    >
      {proposal && !held && <FormHelperText sx={{ m: 0, overflowWrap: 'anywhere' }}>{tr(t, proposal.source)}</FormHelperText>}
    </ChoiceRow>
    {held && <Grid size={{ xs: 12 }}>
      <ProposalBox
        title={t('mgmt.config.zones.fork.modeProposed', 'Proposed for {{name}}: {{mode}}', { name: zone, mode: t(LABELS[proposal.mode][0], LABELS[proposal.mode][1]) })}
        source={tr(t, proposal.source)}
        onUse={() => { formik.setFieldValue('HTTPSCertificateMode', proposal.mode); setMark(proposal.mode); }}
        useLabel={t('mgmt.config.zones.fork.useMode', 'Use {{mode}}', { mode: t(LABELS[proposal.mode][0], LABELS[proposal.mode][1]) })}
      />
    </Grid>}
  </>;
};

// The DNS provider the domain's name servers belong to, asked once the name
// was left, for a new public name with Let's Encrypt.
export const ForkZoneProvider = ({ formik, isNew }) => {
  const { t } = useTranslation();
  const zone = cleanZone(formik.values.Zone);
  const [info, setInfo] = React.useState(null);
  const [failure, setFailure] = React.useState('');
  const asked = React.useRef('');
  const left = Boolean(formik.touched.Zone);
  const wanted = isNew && left && isZoneName(zone) && !isLocalZone(zone) && formik.values.HTTPSCertificateMode === 'LETSENCRYPT';

  React.useEffect(() => {
    if (!wanted || asked.current === zone) return undefined;
    let gone = false;
    const timer = setTimeout(async () => {
      asked.current = zone;
      setFailure('');
      const res = await API.config.certificatesDomain(zone);
      if (gone) return;
      if (!res.ok) {
        setInfo(null);
        setFailure(res.status === 429
          ? t('mgmt.certs.add.tooMany', 'Too many lookups in the last hour: try again later.')
          : (res.message || t('mgmt.certs.add.lookupError', 'The domain could not be looked up.')));
        return;
      }
      setInfo(res.data);
      // a provider is filled in only while none is chosen
      if (res.data.provider && !formik.values.DNSChallengeProvider) {
        formik.setValues({ ...formik.values, DNSChallengeProvider: res.data.provider, UseWildcardCertificate: true });
      }
    }, 600);
    return () => { gone = true; clearTimeout(timer); };
  }, [wanted, zone]);

  if (!isNew || !info || info.domain !== zone) {
    return failure && wanted ? <Grid size={{ xs: 12 }}><FormHelperText>{failure}</FormHelperText></Grid> : null;
  }
  const found = proposalFor(info);
  const providerName = info.providerName || info.provider;
  const chosen = formik.values.DNSChallengeProvider;
  return <Grid size={{ xs: 12 }}>
    {info.provider && chosen && chosen !== info.provider
      ? <ProposalBox
        title={t('mgmt.config.zones.fork.providerProposed', 'Proposed DNS provider: {{provider}}', { provider: providerName })}
        source={found && tr(t, [found.source[0], found.source[1], found.source[2]])}
        onUse={() => formik.setFieldValue('DNSChallengeProvider', info.provider)}
        useLabel={t('mgmt.config.zones.fork.useProvider', 'Use {{provider}}', { provider: providerName })}
      />
      : <FormHelperText sx={{ m: 0, overflowWrap: 'anywhere' }}>
        {found && tr(t, [found.source[0], found.source[1], found.source[2]])}
        {' '}
        {info.provider
          ? t('mgmt.config.zones.fork.providerFilled', 'The provider below was filled in from that: enter the token or key it asks for.')
          : t('mgmt.config.zones.fork.providerNone', 'Choose its provider below if Cosmos knows it, or leave it empty: Let\'s Encrypt then checks the domain over port 80.')}
      </FormHelperText>}
  </Grid>;
};
