// Fork addition: the fields of the "New network" and "New volume" dialogs
// (createNetwork.jsx, createVolumes.jsx), guided: names checked with Docker's
// rule and against the ones that exist, the network kind as one choice with a
// sentence each (only those Docker can create here), and for a network on the
// home network (macvlan) the server's own network cards proposed with their
// network (from POST /api/config/http-check: nothing contacted). The rules are
// in utils/smallForms.js; the server checks the network the same way
// (src/docker/fork_network_create.go).
import * as React from 'react';
import { Alert, Chip, FormHelperText, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import ChoiceChips from '../../components/choiceChips';
import { FormField } from '../../components/formField';
import { dockerNameProblem, subnetProblem, interfaceProblem, lanCards } from '../../utils/smallForms';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

// The names that exist, read once when a dialog opens.
export const useTakenNames = (kind, open) => {
  const [taken, setTaken] = React.useState([]);
  React.useEffect(() => {
    if (!open) return undefined;
    let gone = false;
    (kind === 'volume' ? API.docker.volumeList() : API.docker.networkList()).then((res) => {
      if (gone) return;
      const data = res && res.data;
      const list = kind === 'volume' ? ((data && data.Volumes) || data || []) : (data || []);
      setTaken((Array.isArray(list) ? list : []).map((item) => item && item.Name).filter(Boolean));
    }).catch(() => {});
    return () => { gone = true; };
  }, [kind, open]);
  return taken;
};

// What the server knows about its network cards, for macvlan.
const useCards = (wanted) => {
  const [cards, setCards] = React.useState(null);
  React.useEffect(() => {
    if (!wanted || cards) return;
    API.config.httpCheck({ httpPort: '', httpsPort: '', adminWhitelist: [], constellationOnly: false })
      .then((res) => setCards(lanCards(res.data))).catch(() => setCards([]));
  }, [wanted]);
  return cards || [];
};

export const networkErrors = (values, taken) => {
  const errors = {};
  const name = dockerNameProblem(values.name, taken, 'network');
  if (!String(values.name || '').trim()) errors.name = ['global.required', 'Required'];
  else if (name) errors.name = name;
  const subnet = subnetProblem(values.subnet);
  if (subnet) errors.subnet = subnet;
  if (values.driver === 'macvlan') {
    const card = interfaceProblem(values.parentInterface);
    if (card) errors.parentInterface = card;
  }
  return errors;
};

export const ForkNetworkFields = ({ formik, taken }) => {
  const { t } = useTranslation();
  const { values } = formik;
  const macvlan = values.driver === 'macvlan';
  const cards = useCards(macvlan);
  const errors = networkErrors(values, taken);
  const shown = (name) => (formik.submitCount > 0 || formik.touched[name]) && errors[name] ? tr(t, errors[name]) : '';
  const set = (name) => (text) => { formik.setFieldTouched(name, true, false); formik.setFieldValue(name, text); };

  return <Stack spacing={2.5} sx={{ mt: 1 }}>
    <FormField
      id="name"
      label={t('global.nameTitle')}
      required
      value={values.name}
      onChange={set('name')}
      error={shown('name')}
      helperText={t('mgmt.servApps.fork.networkNameHelp', 'For example media-net. Apps on the same network reach each other by their names.')}
    />
    <ChoiceChips
      id="fork-network-driver"
      label={t('mgmt.servApps.fork.networkKind', 'Kind of network')}
      value={macvlan ? 'macvlan' : 'bridge'}
      onChange={(driver) => formik.setValues({ ...values, driver, parentInterface: driver === 'macvlan' ? values.parentInterface : '' })}
      options={[
        { value: 'bridge', label: t('mgmt.servApps.fork.kindBridge', 'Private to this server (usual)'), description: t('mgmt.servApps.fork.kindBridgeHint', 'Apps on it talk to each other and reach the internet; from outside they are reached through the ports they publish or their URL in Cosmos.') },
        { value: 'macvlan', label: t('mgmt.servApps.fork.kindMacvlan', 'On the home network (macvlan)'), description: t('mgmt.servApps.fork.kindMacvlanHint', 'Each app on it gets its own address on your home network, like another device. The server itself cannot reach those apps (a rule of Linux), and the addresses must be ones your router does not hand out.') },
      ]}
    />
    {macvlan && <Stack spacing={1}>
      {cards.length > 0 && <>
        <Typography variant="caption" color="textSecondary">{t('mgmt.servApps.fork.cardsFound', 'The server\'s network cards (click to use one):')}</Typography>
        <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
          {cards.map((card) => <Chip
            key={card.interface}
            size="small"
            clickable
            color={values.parentInterface === card.interface ? 'primary' : 'default'}
            variant={values.parentInterface === card.interface ? 'filled' : 'outlined'}
            label={`${card.interface} · ${card.network}`}
            onClick={() => formik.setValues({ ...values, parentInterface: card.interface, subnet: values.subnet ? values.subnet : card.network })}
            sx={{ maxWidth: '100%' }}
          />)}
        </Stack>
      </>}
      <FormField
        id="parentInterface"
        label={t('mgmt.servapps.networks.list.parentIf')}
        required
        value={values.parentInterface}
        onChange={set('parentInterface')}
        error={shown('parentInterface')}
        helperText={t('mgmt.servApps.fork.parentHelp', 'The server\'s network card on the home network, for example eno1 or eth0.')}
      />
    </Stack>}
    <FormField
      id="subnet"
      label={t('mgmt.servapps.networks.list.subnet')}
      value={values.subnet}
      onChange={set('subnet')}
      error={shown('subnet')}
      placeholder={macvlan ? '192.168.1.0/24' : '172.30.0.0/24'}
      helperText={macvlan
        ? t('mgmt.servApps.fork.subnetMacvlanHelp', 'Your home network, for example 192.168.1.0/24 (proposed from the card picked above).')
        : t('mgmt.servApps.fork.subnetHelp', 'Leave empty: Docker picks a free range. Only for a range of your own, for example 172.30.0.0/24.')}
    />
    {macvlan && <Alert severity="info" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
      {t('mgmt.servApps.fork.macvlanNote', 'Give each app on this network a fixed address outside the range your router hands out (its DHCP range), or two devices can end up with the same address.')}
    </Alert>}
  </Stack>;
};

export const volumeErrors = (values, taken) => {
  const name = dockerNameProblem(values.name, taken, 'volume');
  if (!String(values.name || '').trim()) return { name: ['global.required', 'Required'] };
  return name ? { name } : {};
};

export const ForkVolumeFields = ({ formik, taken }) => {
  const { t } = useTranslation();
  const errors = volumeErrors(formik.values, taken);
  return <Stack spacing={2} sx={{ mt: 1 }}>
    <FormField
      id="name"
      label={t('global.nameTitle')}
      required
      value={formik.values.name}
      onChange={(text) => { formik.setFieldTouched('name', true, false); formik.setFieldValue('name', text); }}
      error={(formik.submitCount > 0 || formik.touched.name) && errors.name ? tr(t, errors.name) : ''}
      helperText={t('mgmt.servApps.fork.volumeNameHelp', 'For example jellyfin-config. Docker keeps the volume on the server\'s system disk, under its own folder; for large data a folder on a data disk is better (an app\'s Storage tab).')}
    />
    <FormHelperText sx={{ m: 0 }}>{t('mgmt.servApps.fork.volumeDriver', 'Kept by Docker on this server (the "local" driver).')}</FormHelperText>
  </Stack>;
};
