// Fork addition: the review of a creation, in words, before anything is made:
// what will be created and published, where each folder of data goes (with
// the door to put it elsewhere), whether the images run on this server, what
// of an imported file is not used, and every access to the server that needs
// an explicit yes. The logic is in utils/dataPlaces.js, composeImport.js and
// sensitiveAccess.js; the server checks the same again.
import * as React from 'react';
import {
  Alert, Button, Checkbox, Chip, Collapse, FormControlLabel, Stack, TextField, Typography,
} from '@mui/material';
import { CheckCircleOutlined, DownOutlined, HddOutlined, RightOutlined, WarningOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { FilePickerButton } from './filePicker';
import * as API from '../api';
import { applyPlaces, dataVolumes, finalPlace, placeOf, proposeDefaults, proposePlaces, reviewSentences, sizeText, text as placeText } from '../utils/dataPlaces';
import { text as noteText } from '../utils/composeImport';
import { sentence as sensitiveSentence } from '../utils/sensitiveAccess';

const useSay = () => {
  const { t } = useTranslation();
  return (s) => (s ? t(s.key, s.text, s.params) : '');
};

const wrap = { overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } };

// One folder of data, with its door.
const PlaceLine = ({ volume, choice, proposal, plan, onChoose, busy }) => {
  const { t } = useTranslation();
  const say = useSay();
  const [open, setOpen] = React.useState(false);
  const [typed, setTyped] = React.useState('');
  const where = finalPlace(volume, choice);
  const place = where.type === 'bind' ? placeOf(where.path, plan && plan.places) : ((plan && plan.places) || []).find((p) => p.kind === 'docker-root');
  const disk = place ? (place.osDisk ? say(placeText('place.system')) : say(placeText('place.on', { disk: place.disk || place.path, free: sizeText(place.free) }))) : '';
  const candidates = ((plan && plan.places) || []).filter((p) => p.kind === 'data-path' || p.kind === 'disk');
  const folder = (base) => `${base.replace(/\/$/, '')}/${volume.service}/${String(volume.target).replace(/\/+$/, '').split('/').pop() || 'data'}`;
  const proposed = proposal && !proposal.keep;
  const reason = proposal ? say(placeText('reason.' + proposal.reason)) : '';

  return <Stack spacing={0.5} sx={{ minWidth: 0 }} data-place={volume.key}>
    <Stack direction="row" spacing={1} sx={{ alignItems: 'flex-start', minWidth: 0 }}>
      <HddOutlined style={{ marginTop: 4 }} />
      <Stack sx={{ minWidth: 0, flex: 1 }}>
        <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
          {say(placeText(where.type === 'bind' ? 'place.bind' : 'place.volume', { service: volume.service, target: volume.target, path: where.path }))}
        </Typography>
        <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
          {[disk, (proposed && (!choice || choice.proposed)) || volume.frozen || (proposal && proposal.reason === 'systemAsked') ? reason : ''].filter(Boolean).join(' · ')}
        </Typography>
      </Stack>
      {!volume.frozen && <Button size="small" disabled={busy} onClick={() => setOpen(!open)} endIcon={open ? <DownOutlined /> : <RightOutlined />} sx={{ flexShrink: 0 }}>
        {t('mgmt.servapps.creation.place.change', 'Change')}
      </Button>}
    </Stack>
    <Collapse in={open} unmountOnExit>
      <Stack spacing={1} sx={{ pl: 3, minWidth: 0 }}>
        <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
          <Chip size="small" variant={choice && choice.keep ? 'filled' : 'outlined'} color={choice && choice.keep ? 'primary' : 'default'}
            label={volume.type === 'volume' ? t('mgmt.servapps.creation.place.keepVolume', 'A Docker volume, as the app has it') : t('mgmt.servapps.creation.place.keepBind', 'As the app has it')}
            onClick={() => onChoose({ keep: true })} />
          {candidates.map((p) => {
            const path = folder(p.path);
            const on = choice && !choice.keep && choice.path === path;
            return <Chip key={p.path} size="small" clickable variant={on ? 'filled' : 'outlined'} color={on ? 'primary' : 'default'}
              label={`${p.path} (${sizeText(p.free)})`} title={path} onClick={() => onChoose({ path })} sx={{ maxWidth: '100%' }} />;
          })}
        </Stack>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0 }}>
          <FilePickerButton select="folder" canCreate value={typed || where.path} onPick={(path) => { if (path) { setTyped(path); onChoose({ path }); } }} />
          <TextField size="small" fullWidth value={typed} placeholder={t('mgmt.servapps.creation.place.other', 'Another folder on the server, for example /mnt/disk2/app')}
            onChange={(e) => setTyped(e.target.value)}
            onBlur={() => { if (typed.trim().startsWith('/')) onChoose({ path: typed.trim() }); }} />
        </Stack>
      </Stack>
    </Collapse>
  </Stack>;
};

// `definition`: the service request as it will be sent (after the choices);
// `base`: before them (for the volumes and proposals); `plan`: the answer of
// /api/install/plan; `notes`: the import's; `proposed`: proposeDefaults';
// `sensitive`: sensitiveAccess(definition); `confirmed`/`onConfirm`: the
// ticks; `choices`/`onChoices`: where the data goes; `anyArch`/`onAnyArch`.
const CreationReview = ({
  definition, base, plan, notes, proposed, frozen, sensitive, confirmed, onConfirm, choices, onChoices, anyArch, onAnyArch, busy, edit,
}) => {
  const { t } = useTranslation();
  const say = useSay();
  const volumes = React.useMemo(() => dataVolumes(base || definition, frozen || []), [base, definition, frozen]);
  const proposals = React.useMemo(() => (plan ? proposePlaces(volumes, plan, null) : {}), [volumes, plan]);
  const dropped = (notes || []).filter((n) => n.kind === 'dropped');
  const changed = (notes || []).filter((n) => n.kind === 'changed');
  const blocked = (notes || []).filter((n) => n.kind === 'blocked');
  const badImages = ((plan && plan.images) || []).filter((i) => i.fits === 'no');
  const [showProposed, setShowProposed] = React.useState(false);

  return <Stack spacing={1.5} sx={{ minWidth: 0 }} data-creation-review>
    {!edit && <Stack spacing={0.5} sx={{ minWidth: 0 }}>
      <Typography variant="subtitle1">{t('mgmt.servapps.creation.title', 'What will be created')}</Typography>
      {reviewSentences(definition).map((line, i) => <Typography key={i} variant="body2" sx={{ overflowWrap: 'anywhere' }} data-review-line>
        <CheckCircleOutlined style={{ marginRight: 6 }} />{say(line)}
      </Typography>)}
      {(proposed || []).length > 0 && <>
        <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setShowProposed(!showProposed)} endIcon={showProposed ? <DownOutlined /> : <RightOutlined />}>
          {t('mgmt.servapps.creation.proposedCount', 'Filled in by Cosmos ({{count}})', { count: proposed.length })}
        </Button>
        <Collapse in={showProposed} unmountOnExit>
          <Stack spacing={0.5} sx={{ pl: 3 }}>
            {proposed.map((p, i) => <Typography key={i} variant="caption" sx={{ overflowWrap: 'anywhere' }}>{say(placeText('proposed.' + p.code, { service: p.service, ...(p.params || {}) }))}</Typography>)}
            <Typography variant="caption" color="textSecondary">{t('mgmt.servapps.creation.proposedDoor', 'Each of these can be changed in the definition below (Advanced).')}</Typography>
          </Stack>
        </Collapse>
      </>}
    </Stack>}

    {!edit && volumes.length > 0 && <Stack spacing={1} sx={{ minWidth: 0 }}>
      <Typography variant="subtitle1">{t('mgmt.servapps.creation.dataTitle', 'Where the data goes')}</Typography>
      {!plan && <Typography variant="caption" color="textSecondary">{t('mgmt.servapps.creation.dataLoading', 'Looking at the server\'s disks…')}</Typography>}
      {volumes.map((volume) => <PlaceLine
        key={volume.key}
        volume={volume}
        plan={plan}
        proposal={proposals[volume.key]}
        choice={choices[volume.key]}
        busy={busy}
        onChoose={(choice) => onChoices((old) => ({ ...old, [volume.key]: choice }))}
      />)}
    </Stack>}

    {badImages.length > 0 && <Alert severity="error" sx={wrap} data-review-arch>
      <Stack spacing={1}>
        {badImages.map((image) => <div key={image.image}>
          {t('mgmt.servapps.creation.arch', '{{image}} is made for {{platforms}} only, and this server runs {{server}}: it would not start here.', {
            image: image.image, platforms: image.platforms.join(', '), server: `${plan.server.os}/${plan.server.arch}`,
          })}
        </div>)}
        <FormControlLabel control={<Checkbox checked={!!anyArch} disabled={busy} onChange={(e) => onAnyArch(e.target.checked)} />}
          label={t('mgmt.servapps.creation.archAnyway', 'Install anyway (only works if the server can emulate that processor)')} />
      </Stack>
    </Alert>}

    {sensitive.length > 0 && <Alert severity="warning" icon={<WarningOutlined />} sx={wrap} data-review-sensitive>
      <Stack spacing={0.5}>
        <div>{t('mgmt.servapps.creation.sensitive', 'This app asks for access to the server that Cosmos never gives by default. Tick each one you accept; without every tick nothing is created.')}</div>
        {sensitive.map((item) => <FormControlLabel key={item.id} sx={{ alignItems: 'flex-start' }}
          control={<Checkbox size="small" checked={confirmed.includes(item.id)} disabled={busy}
            onChange={(e) => onConfirm(e.target.checked ? [...confirmed, item.id] : confirmed.filter((id) => id !== item.id))} />}
          label={<Typography variant="body2" sx={{ pt: 1, overflowWrap: 'anywhere' }}>{say(sensitiveSentence(item))}</Typography>} />)}
      </Stack>
    </Alert>}

    {blocked.length > 0 && <Alert severity="error" sx={wrap} data-review-blocked>
      <div>{t('mgmt.servapps.creation.blocked', 'These lines cannot be created as written. Correct them in the file (or the definition below):')}</div>
      <ul style={{ margin: 0, paddingLeft: 18 }}>{blocked.map((n, i) => <li key={i}>{say(noteText(n.code, n.params))}</li>)}</ul>
    </Alert>}
    {dropped.length > 0 && <Alert severity="warning" sx={wrap} data-review-dropped>
      <div>{t('mgmt.servapps.creation.dropped', 'What Cosmos will not use from this file:')}</div>
      <ul style={{ margin: 0, paddingLeft: 18 }}>{dropped.map((n, i) => <li key={i}>{say(noteText(n.code, n.params))}</li>)}</ul>
    </Alert>}
    {changed.length > 0 && <Alert severity="info" sx={wrap} data-review-changed>
      <div>{t('mgmt.servapps.creation.changed', 'What is done differently from Compose:')}</div>
      <ul style={{ margin: 0, paddingLeft: 18 }}>{changed.map((n, i) => <li key={i}>{say(noteText(n.code, n.params))}</li>)}</ul>
    </Alert>}
  </Stack>;
};

export default CreationReview;

// Why Create waits, in words; "" when it does not.
// An edit is not held for access the container may have had already: the server only asks for what is new.
export const reviewHolds = (t, { notes, sensitive, confirmed, plan, anyArch, edit }) => {
  if ((notes || []).some((n) => n.kind === 'blocked')) {
    return t('mgmt.servapps.creation.holdBlocked', 'Correct the lines above that cannot be created first.');
  }
  if (!edit && (sensitive || []).some((item) => !(confirmed || []).includes(item.id))) {
    return t('mgmt.servapps.creation.holdSensitive', 'Tick each access to the server above to accept it, or remove it from the definition.');
  }
  if (((plan && plan.images) || []).some((i) => i.fits === 'no') && !anyArch) {
    return t('mgmt.servapps.creation.holdArch', 'An image is made for another processor: tick "Install anyway" to try it.');
  }
  return '';
};

// The state of a review: the server's plan (asked once per set of images),
// what Cosmos proposes, where the data goes (proposals until the admin picks)
// and the definition that results. `service` is the definition as the form
// or the file made it; nothing is proposed for an edit.
export const useCreation = (service, { edit, frozen } = {}) => {
  const [plan, setPlan] = React.useState(null);
  const [picked, setPicked] = React.useState({});
  const images = React.useMemo(() => [...new Set(Object.values((service && service.services) || {})
    .map((s) => String((s && s.image) || '').trim()).filter(Boolean))].slice(0, 8), [service]);
  const imagesKey = images.join(' ');

  React.useEffect(() => {
    if (!imagesKey || !API.docker.installPlan) return undefined;
    let live = true;
    API.docker.installPlan(images).then((res) => { if (live && res && res.data) setPlan(res.data); }).catch(() => {});
    return () => { live = false; };
  }, [imagesKey]);

  const defaults = React.useMemo(() => (edit ? { definition: service, proposed: [] } : proposeDefaults(service, plan)), [service, plan, edit]);
  const volumes = React.useMemo(() => dataVolumes(defaults.definition, frozen || []), [defaults, frozen]);
  const proposals = React.useMemo(() => (plan && !edit ? proposePlaces(volumes, plan, null) : {}), [volumes, plan, edit]);
  const choices = React.useMemo(() => {
    const result = {};
    volumes.forEach((volume) => {
      const proposal = proposals[volume.key];
      if (proposal) result[volume.key] = proposal.keep ? { keep: true, proposed: true } : { path: proposal.path, proposed: true };
    });
    return { ...result, ...picked };
  }, [volumes, proposals, picked]);
  const final = React.useMemo(() => (edit ? service : applyPlaces(defaults.definition, volumes, choices)), [defaults, volumes, choices, edit, service]);

  return { plan, final, base: defaults.definition, proposed: defaults.proposed, choices, setChoices: setPicked };
};
