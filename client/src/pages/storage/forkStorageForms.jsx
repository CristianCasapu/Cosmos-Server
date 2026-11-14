// Fork addition: the storage dialogs, guided: mount, unmount, merge
// (MergerFS) and the disks of a parity setup (SnapRAID). Hooked into
// upstream's mountDialog.jsx, mountDiskDialog.jsx, mergerDialog.jsx,
// snapRaidDialog.jsx, mounts.jsx and merges.jsx. What can be offered comes
// from the server (GET /api/storage/candidates: the disks with their label,
// size, file system and what holds them; POST /api/storage/unmount-check:
// what uses a mount right now); the logic is in utils/storageForms.js. The
// server refuses the same things, and checks the typed confirmations.
import * as React from 'react';
import {
  Alert, Button, Checkbox, Chip, CircularProgress, Collapse, Dialog, DialogActions, DialogContent, DialogTitle,
  FormHelperText, Stack, Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { DownOutlined, HddOutlined, UpOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import ChoiceChips from '../../components/choiceChips';
import { FormField } from '../../components/formField';
import { keepOnBackdrop } from '../../components/formHelpers';
import {
  sizeText, mountOffers, proposeMountPath, mountPathProblem, OWNER_APPS, ownerChoice, ownerProblem, fstabSentence,
  usersText, unmountNeedsConfirm, POLICIES, policyOf, withPolicy, mergeBranches, freePath,
  parityCandidates, proposeParity, dataCandidates,
} from '../../utils/storageForms';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');
const wrapText = { overflowWrap: 'anywhere', minWidth: 0 };

// The candidates, read when a dialog opens. null while loading; { failed } when unreadable.
export const useCandidates = () => {
  const [data, setData] = React.useState(null);
  React.useEffect(() => {
    let gone = false;
    API.storage.mounts.candidates().then((res) => { if (!gone) setData(res.data || {}); }).catch((err) => { if (!gone) setData({ failed: err.message || true }); });
    return () => { gone = true; };
  }, []);
  return data;
};

const Door = ({ label, open, onToggle, children }) => <Stack spacing={1}>
  <div>
    <Button size="small" onClick={onToggle} endIcon={open ? <UpOutlined /> : <DownOutlined />}>{label}</Button>
  </div>
  <Collapse in={open} unmountOnExit>{children}</Collapse>
</Stack>;

const deviceLabel = (d) => [d.device, d.label ? `“${d.label}”` : '', d.fstype, d.size ? sizeText(d.size) : '', d.model].filter(Boolean).join(' · ');

// Who should own the mounted folder: one choice.
const OwnerChoice = ({ value, onChange }) => {
  const { t } = useTranslation();
  const [other, setOther] = React.useState(ownerChoice(value) === 'other');
  const choice = other ? 'other' : ownerChoice(value);
  const problem = choice === 'other' ? ownerProblem(value) : null;
  return <Stack spacing={1}>
    <ChoiceChips
      id="fork-storage-owner"
      label={t('mgmt.storage.fork.ownerLabel', 'Who may write in it')}
      value={choice}
      onChange={(v) => { setOther(v === 'other'); if (v === 'apps') onChange(OWNER_APPS); if (v === 'root') onChange(''); }}
      options={[
        { value: 'apps', label: t('mgmt.storage.fork.ownerApps', 'The apps (user 1000)'), description: t('mgmt.storage.fork.ownerAppsHint', 'The folder is given to user and group 1000, which most app images (linuxserver.io and others) run as. Usual for a data disk.') },
        { value: 'root', label: t('mgmt.storage.fork.ownerRoot', 'Leave as it is'), description: t('mgmt.storage.fork.ownerRootHint', 'The owner the file system already has is kept (on a new disk: the administrator, root).') },
        { value: 'other', label: t('mgmt.storage.fork.ownerOther', 'Another user'), description: t('mgmt.storage.fork.ownerOtherHint', 'A user name or number, with :group if needed, for example 33:33 for www-data.') },
      ]}
    />
    {choice === 'other' && <FormField id="fork-storage-owner-other" value={value} placeholder="33:33" onChange={onChange} error={problem ? tr(t, problem) : ''} />}
  </Stack>;
};

const PermanentChoice = ({ value, onChange, device }) => {
  const { t } = useTranslation();
  return <ChoiceChips
    id="fork-storage-permanent"
    label={t('mgmt.storage.fork.whenLabel', 'Mount it')}
    value={value ? 'always' : 'now'}
    onChange={(v) => onChange(v === 'always')}
    options={[
      { value: 'always', label: t('mgmt.storage.fork.whenAlways', 'At every start (usual)'), description: tr(t, fstabSentence(true, device)) },
      { value: 'now', label: t('mgmt.storage.fork.whenNow', 'Until the next restart'), description: tr(t, fstabSentence(false)) },
    ]}
  />;
};

// --- mount -------------------------------------------------------------------

// existing: a mount being edited ({device, path, permanent}); disk: a device
// picked on the Disks tab.
export const ForkMountDialog = ({ existing, disk, onClose, onDone }) => {
  const { t } = useTranslation();
  const data = useCandidates();
  const offers = mountOffers(data);
  const takenPaths = ((data && data.mounts) || []).map((m) => m.path).filter((p) => !existing || p !== existing.path);
  const [device, setDevice] = React.useState(existing ? existing.device : (disk || ''));
  const [path, setPath] = React.useState(existing ? existing.path : '');
  const [mark, setMark] = React.useState(null);
  // the mount list spells it "permenant" (upstream)
  const wasPermanent = existing ? !!(existing.permanent || existing.permenant) : true;
  const [permanent, setPermanent] = React.useState(wasPermanent);
  const [owner, setOwner] = React.useState(OWNER_APPS);
  const [netDisk, setNetDisk] = React.useState(false);
  const [door, setDoor] = React.useState(false);
  const [leftOpen, setLeftOpen] = React.useState(false);
  const [tried, setTried] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [failure, setFailure] = React.useState('');

  const picked = ((data && data.devices) || []).find((d) => d.device === device);
  // the folder follows the device while it is empty or still the proposal
  React.useEffect(() => {
    if (existing || !picked) return;
    const proposed = proposeMountPath(picked, takenPaths, (data && data.base) || '/mnt');
    if (!path || path === mark) { setPath(proposed); setMark(proposed); }
  }, [device, data]);

  const pathProblem = mountPathProblem(path);
  const deviceProblem = !device.trim() ? ['mgmt.storage.fork.deviceMissing', 'Pick a disk or a partition above'] : null;
  const ownerBad = ownerProblem(owner);
  const pathTaken = takenPaths.includes(path) ? ['mgmt.storage.fork.pathTaken', 'Something is mounted on this folder already'] : null;

  const save = async () => {
    setTried(true);
    setFailure('');
    if (pathProblem || deviceProblem || ownerBad || pathTaken) return;
    setBusy(true);
    try {
      if (existing) {
        // the edit takes the mount away first; if that fails, nothing else is done
        await API.storage.mounts.unmount({ mountPoint: existing.path, permanent: wasPermanent, confirm: existing.path });
      }
      await API.storage.mounts.mount({ path: device.trim(), mountPoint: path, chown: owner, permanent, netDisk });
      onDone();
    } catch (err) {
      setFailure(err.message || String(err));
      setBusy(false);
    }
  };

  return <Dialog open onClose={keepOnBackdrop(onClose)} maxWidth="sm" fullWidth>
    <DialogTitle>{existing ? t('mgmt.storage.fork.editMountTitle', 'Change the mount of {{path}}', { path: existing.path }) : t('mgmt.storage.fork.mountTitle', 'Mount a disk')}</DialogTitle>
    <DialogContent>
      <Stack spacing={2.5} sx={{ mt: 1, ...wrapText }}>
        <Typography variant="body2" color="textSecondary">
          {t('mgmt.storage.fork.mountIntro', 'Mounting makes a disk\'s files appear in a folder of the server, where apps can use them. Nothing on the disk is changed.')}
        </Typography>

        {!existing && (data === null
          ? <CircularProgress size={20} />
          : data.failed
            ? <Alert severity="warning">{t('mgmt.storage.fork.candidatesFailed', 'The disks could not be listed: enter the device by hand below.')}</Alert>
            : <Stack spacing={1}>
              <Typography variant="subtitle1" component="div">{t('mgmt.storage.fork.whichDisk', 'Which disk or partition')}</Typography>
              {offers.offered.length === 0
                ? <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.noneToMount', 'No disk or partition is waiting to be mounted. A new disk is formatted first (Disks tab).')}</FormHelperText>
                : <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
                  {offers.offered.map((d) => <Chip
                    key={d.device}
                    icon={<HddOutlined />}
                    clickable
                    color={device === d.device ? 'primary' : 'default'}
                    variant={device === d.device ? 'filled' : 'outlined'}
                    label={deviceLabel(d)}
                    onClick={() => { setDevice(d.device); setNetDisk(false); }}
                    sx={{ maxWidth: '100%', height: 'auto', '& .MuiChip-label': { whiteSpace: 'normal', py: 0.5 } }}
                  />)}
                </Stack>}
              {offers.notOffered.length > 0 && <Door
                label={t('mgmt.storage.fork.notOffered', 'Not offered ({{count}})', { count: offers.notOffered.length })}
                open={leftOpen}
                onToggle={() => setLeftOpen(!leftOpen)}
              >
                <Stack spacing={0.5}>
                  {offers.notOffered.map((d) => <FormHelperText key={d.device} sx={{ m: 0 }}>
                    <strong>{d.device}</strong>{d.label ? ` “${d.label}”` : ''}{d.size ? ` · ${sizeText(d.size)}` : ''}: {tr(t, d.reason)}
                  </FormHelperText>)}
                </Stack>
              </Door>}
              {tried && deviceProblem && <FormHelperText error>{tr(t, deviceProblem)}</FormHelperText>}
            </Stack>)}
        {existing && <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.editMountNote', 'Saving unmounts {{device}} and mounts it again with these settings.', { device: existing.device })}</FormHelperText>}

        <FormField
          id="fork-storage-path"
          label={t('mgmt.storage.fork.pathLabel', 'Folder to mount it on')}
          required
          value={path}
          onChange={setPath}
          error={(tried || path) && (pathProblem || pathTaken) ? tr(t, pathProblem || pathTaken) : ''}
          helperText={t('mgmt.storage.fork.pathHelp', 'Below /mnt, where the server keeps its disks; proposed from the disk\'s label. Apps reach the files there, for example /mnt/media.')}
        />
        <PermanentChoice value={permanent} onChange={setPermanent} device={picked} />
        <OwnerChoice value={owner} onChange={setOwner} />

        {!existing && <Door label={t('mgmt.storage.fork.otherDevice', 'Something else (a device by name, a network share)')} open={door} onToggle={() => setDoor(!door)}>
          <Stack spacing={1}>
            <FormField
              id="fork-storage-device"
              label={t('mgmt.storage.mount.whatToMountLabel')}
              value={device}
              placeholder={netDisk ? 'nas.local:/export/media' : '/dev/sdb1'}
              onChange={setDevice}
              helperText={t('mgmt.storage.fork.deviceHelp', 'A device such as /dev/sdb1, or for a network share (NFS) the share such as nas.local:/export/media.')}
            />
            <Stack direction="row" alignItems="center" spacing={1}>
              <Checkbox id="fork-storage-net" checked={netDisk} onChange={(e) => setNetDisk(e.target.checked)} />
              <label htmlFor="fork-storage-net"><Typography variant="body2">{t('mgmt.storage.fork.netDisk', 'It is a network share (NFS): mounted once the network is up')}</Typography></label>
            </Stack>
          </Stack>
        </Door>}

        {failure && <Alert severity="error" sx={{ '& .MuiAlert-message': wrapText }}>{failure}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
      <Button onClick={onClose}>{t('global.cancelAction')}</Button>
      <LoadingButton variant="contained" loading={busy} onClick={save}>{existing ? t('global.saveAction') : t('global.mount')}</LoadingButton>
    </DialogActions>
  </Dialog>;
};

// --- unmount -----------------------------------------------------------------

// mount: {path, device, permanent}; merged: a MergerFS folder.
export const ForkUnmountDialog = ({ mount, merged, onClose, onDone }) => {
  const { t } = useTranslation();
  const [check, setCheck] = React.useState(null);
  const [checkFailed, setCheckFailed] = React.useState('');
  const [forGood, setForGood] = React.useState(!!merged);
  const [typed, setTyped] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [failure, setFailure] = React.useState('');

  React.useEffect(() => {
    API.storage.mounts.unmountCheck(mount.path).then((res) => {
      setCheck(res.data || {});
      if (res.data && !res.data.permanent && !merged) setForGood(false);
    }).catch((err) => setCheckFailed(err.message || 'failed'));
  }, [mount.path]);

  const permanentNow = check ? !!check.permanent : !!(mount.permanent || mount.permenant);
  const permanent = permanentNow && forGood;
  const needConfirm = unmountNeedsConfirm(check, permanent);
  const users = usersText(check);
  const confirmed = typed.trim() === mount.path || typed.trim() === mount.path.split('/').pop();

  const save = async () => {
    setFailure('');
    if (needConfirm && !confirmed) return;
    setBusy(true);
    try {
      await API.storage.mounts.unmount({ mountPoint: mount.path, permanent, confirm: needConfirm ? mount.path : '' });
      onDone();
    } catch (err) {
      setFailure(err.message || String(err));
      setBusy(false);
    }
  };

  return <Dialog open onClose={keepOnBackdrop(onClose)} maxWidth="sm" fullWidth>
    <DialogTitle>{merged ? t('mgmt.storage.fork.unmergeTitle', 'Remove the merged folder {{path}}', { path: mount.path }) : t('mgmt.storage.fork.unmountTitle', 'Unmount {{path}}', { path: mount.path })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ mt: 1, ...wrapText }}>
        <Typography variant="body2">
          {merged
            ? t('mgmt.storage.fork.unmergeIntro', 'The folder {{path}} stops showing the files of its disks. The files stay on each disk, in its own folder.', { path: mount.path })
            : t('mgmt.storage.fork.unmountIntro', 'The files of {{device}} stop appearing in {{path}}. Nothing on the disk is erased.', { device: mount.device, path: mount.path })}
        </Typography>
        {check === null && !checkFailed && <Stack direction="row" spacing={1} alignItems="center"><CircularProgress size={16} /><Typography variant="body2">{t('mgmt.storage.fork.checkingUse', 'Looking at what uses it…')}</Typography></Stack>}
        {checkFailed && <Alert severity="warning" sx={{ '& .MuiAlert-message': wrapText }}>{t('mgmt.storage.fork.checkFailed', 'Cosmos could not tell what uses it: {{reason}}', { reason: checkFailed })}</Alert>}
        {check && check.system && <Alert severity="error">{t('mgmt.storage.fork.systemMount', 'This holds the system or Cosmos itself: it cannot be unmounted from here.')}</Alert>}
        {users && <Alert severity="warning" sx={{ '& .MuiAlert-message': wrapText }}>{tr(t, users)}</Alert>}
        {check && !users && !check.system && <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.notInUse', 'Nothing uses it right now.')}</FormHelperText>}
        {permanentNow && !merged && <ChoiceChips
          id="fork-storage-forgood"
          label={t('mgmt.storage.fork.untilLabel', 'For how long')}
          value={forGood ? 'good' : 'now'}
          onChange={(v) => setForGood(v === 'good')}
          options={[
            { value: 'now', label: t('mgmt.storage.fork.untilRestart', 'Until the next start'), description: t('mgmt.storage.fork.untilRestartHint', 'It stays in /etc/fstab: the next start of the server mounts it again.') },
            { value: 'good', label: t('mgmt.storage.fork.untilGood', 'For good'), description: t('mgmt.storage.fork.untilGoodHint', 'Its line in /etc/fstab is removed and the empty folder too: it stays unmounted until you mount it again here.') },
          ]}
        />}
        {needConfirm && <FormField
          id="fork-storage-confirm"
          label={t('mgmt.storage.fork.typeToConfirm', 'Type {{path}} to confirm', { path: mount.path })}
          value={typed}
          onChange={setTyped}
          autoComplete="off"
          error={typed && !confirmed ? t('mgmt.storage.fork.typedWrong', 'Type the folder exactly as shown') : ''}
          helperText={t('mgmt.storage.fork.confirmWhy', 'Asked because it is in use or because it is removed from /etc/fstab. The server checks it too.')}
        />}
        {failure && <Alert severity="error" sx={{ '& .MuiAlert-message': wrapText }}>{failure}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
      <Button onClick={onClose}>{t('global.cancelAction')}</Button>
      <LoadingButton variant="contained" color="error" loading={busy} disabled={(check && check.system) || (needConfirm && !confirmed) || (check === null && !checkFailed)} onClick={save}>
        {merged ? t('mgmt.storage.fork.unmergeButton', 'Remove the merge') : t('global.unmount')}
      </LoadingButton>
    </DialogActions>
  </Dialog>;
};

// --- merge -------------------------------------------------------------------

const MountRow = ({ m, checked, onChange, disabled, reason }) => {
  const { t } = useTranslation();
  return <Stack direction="row" spacing={1} alignItems="flex-start">
    <Checkbox checked={checked} onChange={(e) => onChange(e.target.checked)} disabled={disabled} inputProps={{ 'aria-label': m.path }} sx={{ mt: -0.75 }} />
    <Stack sx={wrapText}>
      <Typography variant="body2" component="div"><strong>{m.path}</strong> · {m.device}</Typography>
      <Typography variant="caption" color={reason ? 'error' : 'textSecondary'}>
        {reason ? tr(t, reason) : t('mgmt.storage.fork.sizeFree', '{{size}}, {{free}} free', { size: sizeText(m.size), free: sizeText(m.free) })}
      </Typography>
    </Stack>
  </Stack>;
};

export const ForkMergeDialog = ({ onClose, onDone }) => {
  const { t } = useTranslation();
  const data = useCandidates();
  const { offered, left } = mergeBranches(data);
  const taken = ((data && data.mounts) || []).map((m) => m.path);
  const [branches, setBranches] = React.useState([]);
  const [path, setPath] = React.useState('');
  const [policy, setPolicy] = React.useState('epmfs');
  const [opts, setOpts] = React.useState('');
  const [owner, setOwner] = React.useState(OWNER_APPS);
  const [permanent, setPermanent] = React.useState(true);
  const [door, setDoor] = React.useState(false);
  const [leftOpen, setLeftOpen] = React.useState(false);
  const [tried, setTried] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [failure, setFailure] = React.useState('');

  React.useEffect(() => { if (data && !path) setPath(freePath(`${data.base || '/mnt'}/storage`, taken)); }, [data]);

  const total = offered.filter((m) => branches.includes(m.path)).reduce((n, m) => n + (Number(m.size) || 0), 0);
  const pathProblem = mountPathProblem(path) || (taken.includes(path) ? ['mgmt.storage.fork.pathTaken', 'Something is mounted on this folder already'] : null)
    || (branches.some((b) => path === b || path.startsWith(b + '/')) ? ['mgmt.storage.fork.pathInBranch', 'The merged folder cannot be one of the disks it merges, or inside one'] : null);
  const branchProblem = branches.length < 2 ? ['mgmt.storage.fork.twoBranches', 'Tick at least two disks'] : null;
  const optsProblem = /^[A-Za-z0-9_.=,:/+-]*$/.test(opts) ? null : ['mgmt.storage.fork.optsRule', 'Options are written like minfreespace=20G,moveonenospc=true'];

  const save = async () => {
    setTried(true);
    setFailure('');
    if (pathProblem || branchProblem || optsProblem || ownerProblem(owner)) return;
    setBusy(true);
    try {
      await API.storage.mounts.merge({ branches, mountPoint: path, chown: owner, permanent, opts: withPolicy(opts, policy) });
      onDone();
    } catch (err) {
      setFailure(err.message || String(err));
      setBusy(false);
    }
  };

  return <Dialog open onClose={keepOnBackdrop(onClose)} maxWidth="sm" fullWidth>
    <DialogTitle>{t('mgmt.storage.mergeTitle')}</DialogTitle>
    <DialogContent>
      <Stack spacing={2.5} sx={{ mt: 1, ...wrapText }}>
        <Typography variant="body2" color="textSecondary">
          {t('mgmt.storage.fork.mergeIntro', 'A merge shows the files of several disks in one folder, as if they were one big disk. Each file stays whole on one disk: losing a disk loses only its own files.')}
        </Typography>
        {data === null ? <CircularProgress size={20} /> : data.failed
          ? <Alert severity="warning">{t('mgmt.storage.fork.candidatesFailedMerge', 'The disks could not be listed.')}</Alert>
          : <Stack spacing={1}>
            <Typography variant="subtitle1" component="div">{t('mgmt.storage.fork.branchesLabel', 'Disks to merge')}</Typography>
            {offered.length < 2 && <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.needTwo', 'A merge needs two mounted data disks or more; mount them first (Mounts tab).')}</FormHelperText>}
            {offered.map((m) => <MountRow key={m.path} m={m} checked={branches.includes(m.path)} onChange={(on) => setBranches(on ? [...branches, m.path] : branches.filter((b) => b !== m.path))} />)}
            {left.length > 0 && <Door label={t('mgmt.storage.fork.notOffered', 'Not offered ({{count}})', { count: left.length })} open={leftOpen} onToggle={() => setLeftOpen(!leftOpen)}>
              <Stack spacing={0.5}>{left.map((m) => <FormHelperText key={m.path} sx={{ m: 0 }}><strong>{m.path}</strong>: {tr(t, m.reason)}</FormHelperText>)}</Stack>
            </Door>}
            {branches.length > 0 && <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.mergeTotal', '{{count}} disks, {{size}} together', { count: branches.length, size: sizeText(total) })}</FormHelperText>}
            {tried && branchProblem && <FormHelperText error>{tr(t, branchProblem)}</FormHelperText>}
          </Stack>}
        <FormField
          id="fork-merge-path"
          label={t('mgmt.storage.fork.mergePathLabel', 'Merged folder')}
          required
          value={path}
          onChange={setPath}
          error={(tried || path) && pathProblem ? tr(t, pathProblem) : ''}
          helperText={t('mgmt.storage.fork.mergePathHelp', 'Where the merged files appear, below /mnt, for example /mnt/storage. Give apps this folder, not the disks.')}
        />
        <ChoiceChips
          id="fork-merge-policy"
          label={t('mgmt.storage.fork.policyLabel', 'Where new files go')}
          value={policy}
          onChange={setPolicy}
          options={POLICIES.map((p) => ({ value: p.value, label: tr(t, p.label), description: tr(t, p.hint) }))}
        />
        <PermanentChoice value={permanent} onChange={setPermanent} />
        <OwnerChoice value={owner} onChange={setOwner} />
        <Door label={t('mgmt.storage.fork.mergeOptions', 'More MergerFS options')} open={door || Boolean(optsProblem)} onToggle={() => setDoor(!door)}>
          <FormField
            id="fork-merge-opts"
            value={opts}
            placeholder="minfreespace=20G"
            onChange={(text) => { setOpts(text); const p = policyOf(text); if (p && POLICIES.some((x) => x.value === p)) setPolicy(p); }}
            error={optsProblem ? tr(t, optsProblem) : ''}
            helperText={t('mgmt.storage.fork.optsHelp', 'Added to Cosmos\' own options, separated by commas, for example minfreespace=20G (a disk with less free space takes no new files). The choice above sets category.create.')}
          />
        </Door>
        {failure && <Alert severity="error" sx={{ '& .MuiAlert-message': wrapText }}>{failure}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
      <Button onClick={onClose}>{t('global.cancelAction')}</Button>
      <LoadingButton variant="contained" loading={busy} onClick={save}>{t('mgmt.storage.mergeButton')}</LoadingButton>
    </DialogActions>
  </Dialog>;
};

// --- parity (SnapRAID) ---------------------------------------------------------

const nextDataName = (map) => {
  for (let i = 1; i < 100; i += 1) if (!Object.prototype.hasOwnProperty.call(map, `d${i}`)) return `d${i}`;
  return `d${Date.now() % 1000}`;
};

// The data and parity disks of snapRaidDialog.jsx's form: formik.values.data
// ({name: path}) and formik.values.parity ([path]).
export const ForkParityDisks = ({ formik }) => {
  const { t } = useTranslation();
  const data = useCandidates();
  const dataMap = formik.values.data || {};
  const dataPaths = Object.values(dataMap).filter(Boolean);
  const parity = formik.values.parity || [];
  const [mark, setMark] = React.useState(null);
  const proposal = data && !data.failed ? proposeParity(data, dataPaths) : null;

  // the parity follows the data while it is empty or still the proposal
  React.useEffect(() => {
    if (!proposal) return;
    if (parity.length === 0 || (parity.length === 1 && parity[0] === mark)) {
      if (parity[0] !== proposal.path) formik.setFieldValue('parity', [proposal.path]);
      setMark(proposal.path);
    }
  }, [proposal && proposal.path]);

  if (data === null) return <CircularProgress size={20} />;
  if (data.failed) return <Alert severity="warning">{t('mgmt.storage.fork.candidatesFailedMerge', 'The disks could not be listed.')}</Alert>;

  const setData = (path, on) => {
    const next = { ...dataMap };
    if (on) next[nextDataName(next)] = path;
    else Object.keys(next).forEach((k) => { if (next[k] === path) delete next[k]; });
    const nextParity = parity.filter((p) => p !== path);
    formik.setValues({ ...formik.values, data: next, parity: nextParity });
  };
  const setParity = (path, on) => formik.setFieldValue('parity', on ? [...parity, path] : parity.filter((p) => p !== path));
  const dataRows = dataCandidates(data, dataPaths, parity);
  const parityRows = parityCandidates(data, dataPaths, parity);
  const largest = dataRows.filter((m) => dataPaths.includes(m.path)).reduce((n, m) => Math.max(n, Number(m.size) || 0), 0);

  return <Stack spacing={2}>
    <Stack spacing={1}>
      <Typography variant="subtitle1" component="div">{t('mgmt.storage.fork.dataDisks', 'Disks to protect (data)')}</Typography>
      <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.dataDisksHelp', 'Two or more mounted data disks, each on a disk of its own. Their files stay as they are; the parity lets a lost disk be rebuilt.')}</FormHelperText>
      {dataRows.length === 0 && <FormHelperText sx={{ m: 0 }}>{t('mgmt.storage.fork.noDataDisks', 'No mounted data disk: mount the disks first (Mounts tab).')}</FormHelperText>}
      {dataRows.map((m) => <MountRow key={m.path} m={m} checked={dataPaths.includes(m.path)} disabled={!m.ok && !dataPaths.includes(m.path)} reason={m.ok ? null : m.reason} onChange={(on) => setData(m.path, on)} />)}
      {Object.entries(dataMap).filter(([, p]) => p && !dataRows.some((m) => m.path === p)).map(([k, p]) => <FormHelperText key={k} error sx={{ m: 0 }}>
        {t('mgmt.storage.fork.dataGone', '{{path}} is in this setup but not mounted as a data disk now', { path: p })}
      </FormHelperText>)}
    </Stack>
    <Stack spacing={1}>
      <Typography variant="subtitle1" component="div">{t('mgmt.storage.fork.parityDisk', 'Parity disk')}</Typography>
      <FormHelperText sx={{ m: 0 }}>
        {largest
          ? t('mgmt.storage.fork.parityHelpSize', 'At least as large as the largest data disk ({{size}}) and on a disk of its own. It holds only the parity: do not use it for files.', { size: sizeText(largest) })
          : t('mgmt.storage.fork.parityHelp', 'Pick the data disks first: the parity disk must be at least as large as the largest of them.')}
      </FormHelperText>
      {proposal && parity[0] === proposal.path && parity[0] === mark && <FormHelperText sx={{ m: 0 }}>
        {t('mgmt.storage.fork.parityProposed', 'Proposed: {{path}}, the smallest disk that is large enough (the larger ones stay free for data).', { path: proposal.path })}
      </FormHelperText>}
      {parityRows.filter((m) => !dataPaths.includes(m.path)).map((m) => <MountRow key={m.path} m={m} checked={parity.includes(m.path)} disabled={!m.ok && !parity.includes(m.path)} reason={m.ok ? null : m.reason} onChange={(on) => setParity(m.path, on)} />)}
    </Stack>
  </Stack>;
};
