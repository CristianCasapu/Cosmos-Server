import * as React from 'react';
import MainCard from '../../../components/MainCard';
import { Alert, Button, Chip, Collapse, Divider, Stack, TextField, useMediaQuery } from '@mui/material';
import HostChip from '../../../components/hostChip';
import { RouteMode, RouteSecurity } from '../../../components/routeComponents';
import { getFaviconURL } from '../../../utils/routes';
import * as API from '../../../api';
import { AppstoreOutlined, ArrowUpOutlined, BulbTwoTone, CheckOutlined, ClockCircleOutlined, DashboardOutlined, DeleteOutlined, DownOutlined, LockOutlined, PlusCircleOutlined, SyncOutlined, UpOutlined } from "@ant-design/icons";
import PrettyTabbedView from '../../../components/tabbedView/tabbedView';
import Back from '../../../components/back';
import { useParams } from 'react-router';
import ContainerOverview from './overview';
import Logs from './logs';
import DockerContainerSetup from './setup';
import NetworkContainerSetup from './network';
import VolumeContainerSetup from './volumes';
import DockerTerminal from './terminal';
import { Link } from 'react-router-dom';
import { smartDockerLogConcat, tryParseProgressLog } from '../../../utils/docker';
import { LoadingButton } from '@mui/lab';
import LogLine from '../../../components/logLine';
import Highlighter from '../../../components/third-party/Highlighter';
import { useTranslation } from 'react-i18next';
import { InstallExposure } from '../../../components/exposure'; // Fork addition: the internet, proposed and optional
import CreationReview, { reviewHolds, useCreation } from '../../../components/creationReview'; // Fork addition
import { sensitiveAccess } from '../../../utils/sensitiveAccess'; // Fork addition
import { StartProblem } from '../../../components/startProblem'; // Fork addition (plan 43): a new app that does not start
import { START_MARKER, parseStartCheck, repairDefinition, withoutStartCheck } from '../../../utils/services'; // Fork addition (plan 43)

import Editor from 'react-simple-code-editor';
import { highlight, languages } from 'prismjs/components/prism-core';
import 'prismjs/components/prism-json';
import 'prismjs/components/prism-yaml';
import 'prismjs/themes/prism-okaidia.css';

const preStyle = {
  backgroundColor: '#000',
  color: '#fff',
  padding: '10px',
  borderRadius: '5px',
  overflow: 'auto',
  maxHeight: '520px',
  maxWidth: '100%',
  width: '100%',
  margin: '0',
  position: 'relative',
  fontSize: '12px',
  fontFamily: 'monospace',
  whiteSpace: 'pre-wrap',
  wordWrap: 'break-word',
  wordBreak: 'break-all',
  lineHeight: '1.5',
  boxShadow: '0 0 10px rgba(0,0,0,0.5)',
  border: '1px solid rgba(255,255,255,0.1)',
  boxSizing: 'border-box',
  marginBottom: '10px',
  marginTop: '10px',
  marginLeft: '0',
  marginRight: '0',
  display: 'block',
  textAlign: 'left',
  verticalAlign: 'baseline',
  opacity: '1',
}

// Fork addition: `notes` are what an imported file could not take as written (utils/composeImport.js)
const NewDockerService = ({service, refresh, edit, notes, holdReason}) => {
  const { t, i18n } = useTranslation();
  const { containerName } = useParams();
  const [container, setContainer] = React.useState(null);
  const [log, setLog] = React.useState([]);
  const [isDone, setIsDone] = React.useState(false);
  const [failed, setFailed] = React.useState(''); // Fork addition: a failed creation gives the button back, with the reason
  const preRef = React.useRef(null);
  // Fork addition (plan 43): what the server's start check said after the creation, and the repairs made
  const [startCheck, setStartCheck] = React.useState(null);
  const [watching, setWatching] = React.useState(false);
  const [repairs, setRepairs] = React.useState(0);
  const streamed = React.useRef('');
  const screenMin = useMediaQuery((theme) => theme.breakpoints.up('sm'))

  let installer = {...service['cosmos-installer']};
  service = {...service};
  delete service['cosmos-installer'];
  delete service['x-cosmos-installer'];

  // Fork addition: the review (components/creationReview.jsx) proposes what the server can decide and where the
  // data goes; the definition below follows it until the admin edits it by hand
  const frozen = (installer && installer['frozen-volumes']) || [];
  const creation = useCreation(service, { edit, frozen });
  const [edited, setEdited] = React.useState(null);
  const [showDefinition, setShowDefinition] = React.useState(!!edit);
  const [confirmed, setConfirmed] = React.useState([]);
  const [anyArch, setAnyArch] = React.useState(false);
  const dockerCompose = edited !== null ? edited : JSON.stringify(creation.final, null, 2);
  const setDockerCompose = setEdited;
  let current = creation.final;
  try { current = JSON.parse(dockerCompose); } catch (e) { /* said on Create */ }
  const sensitive = sensitiveAccess(current, creation.plan && creation.plan.protected);
  const hold = holdReason || reviewHolds(t, { notes, sensitive, confirmed, plan: creation.plan, anyArch, edit });

  const create = (repaired) => {
    // Fork addition: a definition that is not JSON or YAML is said, not thrown; a failure ends the wait
    let definition;
    try {
      definition = repaired || JSON.parse(dockerCompose); // Fork addition (plan 43): the repair sends its definition
    } catch (e) {
      setFailed(t('mgmt.servapps.create.notJson', 'The definition above is not valid JSON: {{error}}', { error: e.message }));
      return;
    }
    setFailed('');
    setLog([
      'Creating Service...                              ',
    ])
    streamed.current = '';
    setStartCheck(null);
    API.docker.createService(definition, (newlog) => {
      // Fork addition (plan 43): the start check's line is read, not shown
      streamed.current = (streamed.current + newlog).slice(-262144);
      if (newlog.includes('Checking that the new app starts')) setWatching(true);
      const check = streamed.current.includes(START_MARKER) ? parseStartCheck(streamed.current) : null;
      if (check) { setStartCheck(check); setWatching(false); streamed.current = ''; }
      newlog = withoutStartCheck(newlog);
      if (!newlog.trim()) return;
      setLog((old) => smartDockerLogConcat(old, newlog));
      if (preRef.current) preRef.current.scrollTop = preRef.current.scrollHeight;
      if (newlog.includes('[OPERATION SUCCEEDED]')) {
        setIsDone(true);

        refresh && refresh();
      } else if (newlog.includes('[OPERATION FAILED]')) {
        const reason = newlog.replace(/^.*\[OPERATION FAILED\][.\s]*/, '').trim();
        setFailed(reason && !/rolled ?back/i.test(reason) ? reason : t('mgmt.servapps.create.failed', 'The creation failed and what was made has been removed again: the reason is in the log below.'));
      }
    }, !!edit || !!repaired /* Fork addition (plan 43): the repair replaces what the creation made */, sensitiveAccess(definition, creation.plan && creation.plan.protected).map((item) => item.id).filter((id) => confirmed.includes(id)), anyArch)
      .catch((e) => setFailed(e.message || String(e)))
      .finally(() => setWatching(false)); // Fork addition (plan 43)
  }

  // Fork addition (plan 43): the variables the image named, set, and the same creation again
  const repair = (report, values) => {
    const next = repairDefinition(current, report.name, values);
    setEdited(JSON.stringify(next, null, 2));
    setRepairs(repairs + 1);
    setIsDone(false);
    setLog([]);
    create(next);
  };
  const failing = ((startCheck && startCheck.containers) || []).filter((r) => r.failing);

  let isJSON = false;
  if(dockerCompose.trim().startsWith('{') && dockerCompose.trim().endsWith('}')) {
    isJSON = true;
  }

  return   <div style={{ maxWidth: '1000px', width: '100%', margin: '', position: 'relative' }}>
    <MainCard title={edit ? t('mgmt.servapps.container.compose.editServiceTitle') : t('mgmt.servapps.container.compose.createServiceButton')}>
    <Stack spacing={1}>
      {/* Fork addition: optionally open published ports on the router once the app exists (off by default) */}
      {/* Fork addition: the review in words, the data places, the confirmations (components/creationReview.jsx) */}
      <CreationReview definition={current} base={creation.base} plan={creation.plan} notes={notes} proposed={creation.proposed} frozen={frozen}
        sensitive={sensitive} confirmed={confirmed} onConfirm={setConfirmed} choices={creation.choices} onChoices={creation.setChoices}
        anyArch={anyArch} onAnyArch={setAnyArch} busy={!!log.length && !failed} edit={edit} />
      {!edit && <InstallExposure service={current} definition={dockerCompose} done={isDone} />}
      {failed && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }} data-create-failed>{failed}</Alert>}
      {hold && !isDone && !log.length && <Alert severity="info" sx={{ overflowWrap: 'anywhere' }} data-create-hold>{hold}</Alert>}
      {!isDone && <LoadingButton 
        disabled={!!hold}
        onClick={() => create() /* Fork addition (plan 43): not the click event as the definition */}
        variant="contained"
        color="primary"
        fullWidth
        className={edit ? '' : 'shinyButton'}
        loading={!!log.length && !isDone && !failed}
        startIcon={edit ? <SyncOutlined /> : <PlusCircleOutlined />}
      >{edit ? t('global.edit'): t('global.createAction')}</LoadingButton>}
      {/* Fork addition (plan 43): the start check: watching, then why a new container does not run, and the repair */}
      {isDone && watching && <Alert severity="info" data-start-watching>{t('mgmt.services.start.watching', 'Created. Cosmos watches it for up to 30 seconds to see that it starts…')}</Alert>}
      {failing.map((report) => <StartProblem key={report.name} report={report} rounds={repairs} busy={!!log.length && !isDone && !failed} onApply={(values) => repair(report, values)} />)}
      {isDone && !failing.length && <Stack spacing={1}>
        <Alert severity="success">{t('mgmt.servapps.container.compose.createServiceSuccess')}</Alert>
        {installer && installer['post-install'] && installer['post-install'].map(m =>{
          return <Alert severity={m.type}>{
              installer?.translation?.[i18n?.resolvedLanguage]?.[m.key] || 
              installer?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.[m.key] || 
              m.label
          }</Alert>
        })}
      </Stack>}
      

      {(edit || failed) && !isDone && log.length ? <Button onClick={() => { setLog([]); setFailed(''); }}>{t('global.backAction')}</Button> : null}

      {/* Fork addition: the definition is the advanced door, open for an edit */}
      {!log.length && !edit && <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setShowDefinition(!showDefinition)} data-definition-door>
        {showDefinition ? t('mgmt.servapps.creation.hideDefinition', 'Hide the definition') : t('mgmt.servapps.creation.showDefinition', 'The definition (advanced)')}
      </Button>}
      {!log.length && edited !== null && !edit && <Alert severity="info" sx={{ overflowWrap: 'anywhere' }} action={<Button size="small" onClick={() => setEdited(null)}>{t('mgmt.servapps.creation.undoEdits', 'Undo my edits')}</Button>}>
        {t('mgmt.servapps.creation.edited', 'You changed the definition by hand: it is created as written, and changes made above no longer reach it.')}
      </Alert>}
      {log.length ? <pre style={preStyle} ref={preRef}>
        {log.map((l) => {
          return <LogLine message={tryParseProgressLog(l)} docker isMobile={!screenMin} />
        })}
      </pre> :
      <Collapse in={showDefinition || !!edit}><div>
        <Editor
          value={dockerCompose}
          placeholder={t('mgmt.servapps.pasteComposeButton.pasteComposePlaceholder')}
          onValueChange={code => setDockerCompose(code)}
          highlight={code => highlight(code, isJSON ? languages.json : languages.yaml)}
          padding={10}
          tabSize={2}
          insertSpaces={true}
          style={{
            fontFamily: '"Fira code", "Fira Mono", monospace',
            fontSize: 12,
            backgroundColor: '#000',
          }}
        />
      </div></Collapse>
      }
    </Stack>
  </MainCard>
  </div>;
}

export default NewDockerService;