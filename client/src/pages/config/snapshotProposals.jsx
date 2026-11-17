// Fork addition (plan 47): what the incremental backup card (snapshots.jsx)
// proposes. The destination: a folder "snapshots" on the data disks, with
// their free space (from POST /api/install/plan, which lists the data disks);
// the best one goes into an empty field by itself, said under it, and never
// replaces a value typed or saved. The exclusions: a bounded look into a
// folder that is backed up (POST /api/snapshots/look) offers what it found as
// ticks; the text field stays the place to type patterns by hand. The logic
// is utils/snapshotExcludes.js.
import * as React from 'react';
import { Alert, Button, Checkbox, Chip, FormControlLabel, FormHelperText, Grid, Stack, Typography } from '@mui/material';
import { FolderOpenOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import { ProposalNote } from '../../components/formHelpers';
import { simplifyNumber } from '../dashboard/components/utils';
import { bigEntries, destinationProposals, hasPattern, togglePattern } from '../../utils/snapshotExcludes';

const touch = { minHeight: 44 };
const wrapSx = { height: 'auto', maxWidth: '100%', minHeight: { xs: 44, md: 32 }, // a finger's width on a phone
  '& .MuiChip-label': { whiteSpace: 'normal', overflowWrap: 'anywhere', py: 0.5 } };
const size = (bytes) => simplifyNumber(bytes || 0, 'B');

// The places, asked once per page.
export const useDestinationProposals = (sources) => {
  const [places, setPlaces] = React.useState(null);
  React.useEffect(() => {
    let live = true;
    API.docker.installPlan([])
      .then((res) => { if (live) setPlaces((res && res.data && res.data.places) || []); })
      .catch(() => { if (live) setPlaces([]); });
    return () => { live = false; };
  }, []);
  return React.useMemo(() => (places ? destinationProposals(places, sources) : null), [places, (sources || []).join('\n')]);
};

// Under the destination field: where the value came from, and the other places.
export const DestinationProposals = ({ formik, config, proposals }) => {
  const { t } = useTranslation();
  const [mark, setMark] = React.useState('');
  const value = formik.values.Snapshots_Destination || '';
  const saved = (config && config.Snapshots && config.Snapshots.Destination) || '';

  // an empty field, with nothing saved, takes the best place by itself
  React.useEffect(() => {
    if (!proposals || !proposals.length || saved || value) return;
    setMark(proposals[0].path);
    formik.setFieldValue('Snapshots_Destination', proposals[0].path);
  }, [proposals]);

  if (!proposals || !proposals.length) return null;
  const shown = proposals.slice(0, 4);
  const best = proposals.find((p) => p.path === mark);
  return <Stack spacing={0.75} sx={{ mt: 1 }}>
    {best && value === mark && <ProposalNote>
      <span data-fork-snapshot-destination-note>
        {best.osDisk
          ? t('mgmt.config.snapshots.fork.destOs', 'Proposed: the disk the system runs from ({{free}} free) is the only one found. Another disk keeps the snapshots safe when this one fails.', { free: size(best.free) })
          : t('mgmt.config.snapshots.fork.destBest', 'Proposed: the data disk with the most room that holds none of the folders backed up ({{free}} free of {{total}}). Change it if you prefer another place.', { free: size(best.free), total: size(best.total) })}
      </span>
    </ProposalNote>}
    <Typography variant="caption" color="textSecondary">
      {t('mgmt.config.snapshots.fork.destChips', 'Data disks of this server (click to use one):')}
    </Typography>
    <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }}>
      {shown.map((p) => <Chip
        key={p.path}
        clickable
        size="small"
        color={value === p.path ? 'primary' : 'default'}
        variant={value === p.path ? 'filled' : 'outlined'}
        label={t('mgmt.config.snapshots.fork.destChip', '{{path}} — {{free}} free', { path: p.path, free: size(p.free) })
          + (p.osDisk ? ' ' + t('mgmt.config.snapshots.fork.destSystem', '(system disk)') : '')}
        onClick={() => formik.setFieldValue('Snapshots_Destination', p.path)}
        sx={wrapSx}
        data-fork-snapshot-destination={p.path}
      />)}
    </Stack>
  </Stack>;
};

const KIND = {
  docker: ['mgmt.config.snapshots.fork.kindDocker', 'Docker\'s own data (images, containers): made again from the images; a copy is large and of little use'],
  cache: ['mgmt.config.snapshots.fork.kindCache', 'Caches: the apps make them again'],
  thumbnails: ['mgmt.config.snapshots.fork.kindThumbnails', 'Thumbnails: made again from the pictures and videos'],
  temporary: ['mgmt.config.snapshots.fork.kindTemporary', 'Temporary files: half-finished downloads, transcodes'],
  trash: ['mgmt.config.snapshots.fork.kindTrash', 'Trash: files already deleted'],
};

const Tick = ({ formik, pattern, label, detail }) => {
  const text = formik.values.Snapshots_Excludes || '';
  const on = hasPattern(text, pattern);
  return <Stack>
    <FormControlLabel
      sx={{ ...touch, mr: 0, alignItems: 'center', '& .MuiFormControlLabel-label': { overflowWrap: 'anywhere', minWidth: 0 } }}
      control={<Checkbox
        checked={on}
        onChange={(event) => formik.setFieldValue('Snapshots_Excludes', togglePattern(text, pattern, event.target.checked))}
        slotProps={{ input: { 'data-fork-exclude': pattern } }}
      />}
      label={label}
    />
    {detail && <FormHelperText sx={{ mt: 0, ml: 4, overflowWrap: 'anywhere' }}>{detail}</FormHelperText>}
  </Stack>;
};

// Below the exclusions: a look into each folder that is backed up.
export const ExcludeOffers = ({ formik, sources }) => {
  const { t } = useTranslation();
  const [looks, setLooks] = React.useState({}); // source → { loading, answer, error }
  const look = (source) => {
    setLooks((all) => ({ ...all, [source]: { loading: true } }));
    API.config.snapshotsLook(source)
      .then((res) => setLooks((all) => ({ ...all, [source]: { answer: res && res.data } })))
      .catch((e) => setLooks((all) => ({ ...all, [source]: { error: (e && e.message) || String(e) } })));
  };
  if (!sources.length) return null;
  return <Grid size={{ xs: 12 }}>
    <Stack spacing={1.5}>
      <Typography variant="caption" color="textSecondary">
        {t('mgmt.config.snapshots.fork.lookTitle', 'Cosmos can look into a folder and offer what is usually not worth keeping:')}
      </Typography>
      {sources.map((source) => {
        const state = looks[source] || {};
        const answer = state.answer;
        const big = bigEntries(answer);
        return <Stack key={source} spacing={1} data-fork-snapshot-look={source}>
          <Button
            variant="outlined"
            size="small"
            sx={{ ...touch, alignSelf: 'flex-start', maxWidth: '100%', textAlign: 'left', overflowWrap: 'anywhere' }}
            startIcon={<FolderOpenOutlined />}
            loading={Boolean(state.loading)}
            onClick={() => look(source)}
          >
            {answer
              ? t('mgmt.config.snapshots.fork.lookAgain', 'Look again in {{path}}', { path: source })
              : t('mgmt.config.snapshots.fork.look', 'Look in {{path}}', { path: source })}
          </Button>
          {state.error && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{state.error}</Alert>}
          {answer && <Stack spacing={0.25}>
            {!answer.complete && <FormHelperText>
              {t('mgmt.config.snapshots.fork.incomplete', 'The folder is large: Cosmos looked at the first {{count}} entries, at most four levels deep. The sizes are at least what is shown.', { count: Number(answer.entries || 0).toLocaleString() })}
            </FormHelperText>}
            {(answer.offers || []).map((offer) => {
              const [key, text] = KIND[offer.kind] || KIND.temporary;
              return <Tick
                key={offer.pattern}
                formik={formik}
                pattern={offer.pattern}
                label={<span><code>{offer.pattern}</code> — {t(key, text)}</span>}
                detail={offer.count > 0
                  ? t('mgmt.config.snapshots.fork.found', 'Found {{count}} time(s), {{size}}, for example {{example}}', { count: offer.count, size: size(offer.size), example: (offer.found || []).join(', ') })
                  : t('mgmt.config.snapshots.fork.foundDeep', 'At {{example}}', { example: (offer.found || []).join(', ') })}
              />;
            })}
            {!(answer.offers || []).length && <FormHelperText>
              {t('mgmt.config.snapshots.fork.nothing', 'No cache, thumbnails, temporary files or trash were found in this folder.')}
            </FormHelperText>}
            {big.length > 0 && <>
              <Typography variant="caption" color="textSecondary" sx={{ pt: 1 }}>
                {t('mgmt.config.snapshots.fork.bigTitle', 'The largest folders at the top of {{path}} (tick one to leave it out):', { path: source })}
              </Typography>
              {big.map((entry) => <Tick
                key={entry.pattern}
                formik={formik}
                pattern={entry.pattern}
                label={<span><code>{entry.pattern}</code> — {size(entry.size)}</span>}
              />)}
            </>}
          </Stack>}
        </Stack>;
      })}
    </Stack>
  </Grid>;
};

