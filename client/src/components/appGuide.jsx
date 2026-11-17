// Fork addition (plan 50): "How to use it", the guide of an app, built from
// the app's real addresses (utils/guides.js; the text is src/guides/<kind>.md).
//
// Above the text: where the reader connects from (home, the internet,
// another app of this server: the default is where the visitor is), the
// three addresses, the names of the bucket and the machine (typed values are
// checked; anything that is not plain is refused), the certificate of Cosmos'
// authority when an address needs it, and Print. Then a table of contents
// and one folding section per platform, each with its own link (#id) that
// opens it. Code blocks scroll inside themselves and have Copy; the parts the
// reader replaces are marked. Everything is rendered as React text: the guide
// holds no HTML and none is made.
import * as React from 'react';
import {
  Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, Chip, GlobalStyles, IconButton, Link, Stack, Table, TableBody, TableCell,
  TableHead, TableRow, TextField, Tooltip, Typography,
} from '@mui/material';
import { CopyOutlined, DownOutlined, LinkOutlined, PrinterOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { Link as RouterLink } from 'react-router';
import { useTranslation } from 'react-i18next';

import ChoiceChips from './choiceChips';
import { DownloadFile } from '../api/downloadButton';
import { copyToClipboard } from './appOpen';
import { notify } from '../utils/activity';
import {
  parseGuide, guideFacts, visibleBlocks, visibleInline, visibleSections, renderCode, inlineValue, validBucket, validName, guidePath, PLACES,
} from '../utils/guides';
import s3Guide from '../../../src/guides/s3.md?raw';

const GUIDES = { s3: s3Guide };
const parsed = {};
const guideOf = (kind) => {
  if (!GUIDES[kind]) return null;
  if (!parsed[kind]) parsed[kind] = parseGuide(GUIDES[kind]);
  return parsed[kind];
};

const PLACE_TEXT = {
  home: ['mgmt.guide.place.home', 'Inside my home'],
  internet: ['mgmt.guide.place.internet', 'The internet'],
  container: ['mgmt.guide.place.container', 'Another app on this server'],
};

const useCopy = () => {
  const { t } = useTranslation();
  return (text, what) => copyToClipboard(text)
    .then(() => notify(t('mgmt.guide.copied', 'Copied: {{what}}', { what }), 'success'))
    .catch(() => notify(t('mgmt.guide.copyFailed', 'This browser did not let the page copy. Select the text and copy it by hand.'), 'warning'));
};

// ---------------------------------------------------------------------------
// text

const Inline = ({ items, facts }) => <>{visibleInline(items, facts.flags).map((x, i) => {
  if (x.t === 'code') return <Box key={i} component="code" sx={{ fontFamily: 'monospace', fontSize: '0.92em', px: 0.5, borderRadius: 1, bgcolor: 'action.hover', overflowWrap: 'anywhere' }}>{fillInline(x.text, facts)}</Box>;
  if (x.t === 'strong') return <strong key={i}>{x.text}</strong>;
  if (x.t === 'link') return <Link key={i} href={x.href} target="_blank" rel="noopener noreferrer">{x.text}</Link>;
  if (x.t === 'value') return <Box key={i} component="span" sx={{ fontWeight: 600, overflowWrap: 'anywhere' }} data-value={x.name}>{inlineValue(x.name, facts) || '…'}</Box>;
  return <React.Fragment key={i}>{x.text}</React.Fragment>;
})}</>;

// placeholders inside inline code (`{{endpoint}}/bucket`)
const fillInline = (text, facts) => String(text).replace(/\{\{([a-z_]+)\}\}/g, (m, name) => inlineValue(name, facts) || '…');

const CodeBlock = ({ block, facts }) => {
  const { t } = useTranslation();
  const copy = useCopy();
  const code = renderCode(block, facts);
  if (!code.ok) {
    return <Alert severity="warning" data-code={block.id} data-code-problem={code.problem}>
      {t('mgmt.guide.notBuilt', 'This example could not be built for this app: a value it needs is missing or is not plain ({{problem}}).', { problem: code.problem })}
    </Alert>;
  }
  if (!code.text.trim()) return null;
  const label = block.attrs.file ? block.attrs.file : block.lang;
  return <Box sx={{ position: 'relative', border: 1, borderColor: 'divider', borderRadius: 1, bgcolor: 'action.hover', minWidth: 0 }} data-code={block.id}>
    <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', pl: 1.5, borderBottom: 1, borderColor: 'divider' }}>
      <Typography variant="caption" color="textSecondary" sx={{ fontFamily: 'monospace' }}>{label}</Typography>
      <Tooltip title={t('mgmt.guide.copyBlock', 'Copy this block')}>
        <IconButton aria-label={t('mgmt.guide.copyBlock', 'Copy this block')} onClick={() => copy(code.text, block.attrs.file || t('mgmt.guide.theBlock', 'the block'))} sx={{ width: 44, height: 44 }} data-copy-block={block.id} className="guide-no-print">
          <CopyOutlined />
        </IconButton>
      </Tooltip>
    </Stack>
    <Box component="pre" tabIndex={0} aria-label={label} sx={{ m: 0, p: 1.5, overflowX: 'auto', fontFamily: 'monospace', fontSize: '0.85rem', lineHeight: 1.5, whiteSpace: 'pre', '@media print': { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' } }} data-code-text={block.id}>
      {code.parts.map((p, i) => (p.kind === 'secret'
        ? <Box key={i} component="mark" sx={{ bgcolor: 'warning.light', color: 'text.primary', px: 0.25, borderRadius: 0.5 }} title={t('mgmt.guide.replace', 'Replace this with your own value')}>{p.text}</Box>
        : p.kind === 'value' ? <Box key={i} component="span" sx={{ fontWeight: 600 }}>{p.text}</Box> : <React.Fragment key={i}>{p.text}</React.Fragment>))}
    </Box>
  </Box>;
};

const Blocks = ({ blocks, facts }) => <Stack spacing={1.5}>
  {visibleBlocks(blocks, facts.flags).map((b, i) => {
    if (b.type === 'heading') return <Typography key={i} variant={b.level === 3 ? 'h5' : 'h6'} component={b.level === 3 ? 'h3' : 'h4'} sx={{ pt: 1 }}><Inline items={b.inline} facts={facts} /></Typography>;
    if (b.type === 'para') return <Typography key={i} sx={{ overflowWrap: 'anywhere' }}><Inline items={b.inline} facts={facts} /></Typography>;
    if (b.type === 'list') return <Box key={i} component={b.ordered ? 'ol' : 'ul'} sx={{ m: 0, pl: 3, '& li': { mb: 0.5, overflowWrap: 'anywhere' } }}>
      {b.items.map((item, j) => <li key={j}><Typography component="span"><Inline items={item} facts={facts} /></Typography></li>)}
    </Box>;
    if (b.type === 'note') return <Alert key={i} severity={b.kind === 'warning' ? 'warning' : 'info'} sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}><Inline items={b.inline} facts={facts} /></Alert>;
    if (b.type === 'table') return <Box key={i} sx={{ overflowX: 'auto', minWidth: 0 }}>
      <Table size="small">
        <TableHead><TableRow>{b.head.map((c, j) => <TableCell key={j}><Inline items={c} facts={facts} /></TableCell>)}</TableRow></TableHead>
        <TableBody>{b.rows.map((row, j) => <TableRow key={j}>{row.map((c, k) => <TableCell key={k} sx={{ overflowWrap: 'anywhere', minWidth: 100 }}><Inline items={c} facts={facts} /></TableCell>)}</TableRow>)}</TableBody>
      </Table>
    </Box>;
    if (b.type === 'code') return <CodeBlock key={i} block={b} facts={facts} />;
    return null;
  })}
</Stack>;

// ---------------------------------------------------------------------------
// the page's part

const readHash = () => {
  try { return decodeURIComponent(String(window.location.hash || '').slice(1)); } catch (e) { return ''; }
};

// app: { key, container, routes } with the routes of GET /api/dashboard;
// ctx: useOpenContext(); status: GET /api/status.
export const AppGuide = ({ app, ctx, status, name }) => {
  const { t } = useTranslation();
  const copy = useCopy();
  const kind = ((app && app.routes) || []).map((r) => r.ForkGuide).find(Boolean) || '';
  const guide = guideOf(kind);
  const [choice, setChoice] = React.useState('');
  const [typed, setTyped] = React.useState({ bucket: '', name: '' });
  const [open, setOpen] = React.useState(() => {
    const hash = readHash();
    return hash ? [hash] : [];
  });
  const [printing, setPrinting] = React.useState(false);
  React.useEffect(() => {
    const onHash = () => {
      const hash = readHash();
      if (hash) setOpen((o) => (o.includes(hash) ? o : [...o, hash]));
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);
  React.useEffect(() => {
    const hash = readHash();
    if (!hash || !guide) return undefined;
    const timer = setTimeout(() => { const el = document.getElementById('guide-' + hash); if (el) el.scrollIntoView({ block: 'start' }); }, 300);
    return () => clearTimeout(timer);
  }, [guide]);
  React.useEffect(() => {
    if (!printing) return undefined;
    const timer = setTimeout(() => { window.print(); setPrinting(false); }, 300);
    return () => clearTimeout(timer);
  }, [printing]);

  const facts = React.useMemo(() => (guide ? guideFacts(app, ctx, status, choice, typed) : null), [guide, app, ctx, status, choice, typed]);
  if (!guide || !facts) return null;
  const sections = visibleSections(guide, facts.flags);
  const options = PLACES.filter((p) => facts.places[p]).map((p) => ({ value: p, label: t(...PLACE_TEXT[p]) }));
  const bucketBad = typed.bucket !== '' && !validBucket(typed.bucket);
  const nameBad = typed.name !== '' && !validName(typed.name);
  const link = (id) => window.location.origin + guidePath(app, id);

  return <Stack spacing={2} sx={{ minWidth: 0 }} data-guide={kind} data-place={facts.choice}>
    <GlobalStyles styles={{ '@media print': { '.guide-no-print, header, .MuiDrawer-root, .MuiAppBar-root': { display: 'none !important' } } }} />
    <Stack spacing={1.5} className="guide-no-print-controls">
      {options.length > 0
        ? <ChoiceChips id="guide-place" label={t('mgmt.guide.place.label', 'I am connecting from')} value={facts.choice} options={options} onChange={setChoice}
          helperText={t('mgmt.guide.place.help', 'Every example below is written for this place.')} />
        : <Alert severity="warning">{t('mgmt.guide.noAddress', '{{name}} has no address its clients can use yet. Give it a URL (ServApps, the app, "New URL") and come back.', { name })}</Alert>}
      <Box component="dl" sx={{ m: 0, display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'max-content 1fr' }, columnGap: 2, rowGap: 0.5, '& dd': { m: 0, mb: { xs: 1, sm: 0 }, overflowWrap: 'anywhere', minWidth: 0 } }} data-addresses>
        {PLACES.map((p) => (p === 'container' && !ctx.isAdmin ? null : <React.Fragment key={p}>
          <Typography component="dt" variant="subtitle2">{t(...PLACE_TEXT[p])}</Typography>
          <Typography component="dd" variant="body2" data-address={p}>{facts.places[p]
            ? <>{facts.places[p]}{p === 'internet' && facts.flags.any_cloudflare ? ' — ' + t('mgmt.guide.throughCloudflare', 'through Cloudflare: at most 100 MB per request') : ''}{p === 'container' && facts.values.network ? ' — ' + t('mgmt.guide.onNetwork', 'on the Docker network {{network}}', { network: facts.values.network }) : ''}</>
            : <Box component="span" sx={{ color: 'text.secondary' }}>{p === 'internet' ? t('mgmt.guide.noInternet', 'none: a website hosted elsewhere cannot reach this app') : p === 'home' ? t('mgmt.guide.noHome', 'none') : t('mgmt.guide.noContainer', 'not known')}</Box>}
          </Typography>
        </React.Fragment>))}
      </Box>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5}>
        <TextField size="small" label={t('mgmt.guide.bucket', 'Bucket in the examples')} value={typed.bucket} placeholder={facts.values.bucket}
          onChange={(e) => setTyped({ ...typed, bucket: e.target.value.slice(0, 63) })} error={bucketBad}
          helperText={bucketBad ? t('mgmt.guide.bucketBad', '3 to 63 lower-case letters, digits and dashes; not used until it is') : t('mgmt.guide.bucketHelp', 'One bucket per thing you back up')}
          slotProps={{ htmlInput: { 'data-typed': 'bucket', autoCapitalize: 'none', spellCheck: false } }} sx={{ flex: 1 }} />
        <TextField size="small" label={t('mgmt.guide.machine', 'Name of the machine or site')} value={typed.name} placeholder={facts.values.name}
          onChange={(e) => setTyped({ ...typed, name: e.target.value.slice(0, 31) })} error={nameBad}
          helperText={nameBad ? t('mgmt.guide.machineBad', 'Lower-case letters, digits and dashes; not used until it is') : t('mgmt.guide.machineHelp', 'Names its key and its backups')}
          slotProps={{ htmlInput: { 'data-typed': 'name', autoCapitalize: 'none', spellCheck: false } }} sx={{ flex: 1 }} />
      </Stack>
      <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }} className="guide-no-print">
        {facts.flags.ca && facts.values.ca_pem && <DownloadFile filename="cosmos-ca.pem" content={facts.values.ca_pem + '\n'} label={t('mgmt.guide.caDownload', 'Download Cosmos\' certificate authority')} />}
        <Button variant="outlined" startIcon={<PrinterOutlined />} onClick={() => { setOpen(sections.map((s) => s.id)); setPrinting(true); }} sx={{ minHeight: 44 }} data-guide-print>
          {t('mgmt.guide.print', 'Print or save as PDF')}
        </Button>
        {facts.flags.ca && <Button variant="text" component={RouterLink} to="/cosmos-ui/trust" startIcon={<SafetyCertificateOutlined />} sx={{ minHeight: 44 }}>{t('mgmt.guide.trust', 'Trust it on a computer or phone')}</Button>}
      </Stack>
    </Stack>

    <Blocks blocks={guide.intro} facts={facts} />

    <Box component="nav" aria-label={t('mgmt.guide.contents', 'Contents')} className="guide-no-print">
      <Typography variant="subtitle2" sx={{ mb: 1 }}>{t('mgmt.guide.contents', 'Contents')}</Typography>
      <Stack direction="row" useFlexGap spacing={1} sx={{ flexWrap: 'wrap' }}>
        {sections.map((s) => <Chip key={s.id} component="a" href={'#' + s.id} clickable variant="outlined" label={<Inline items={s.title} facts={facts} />}
          sx={{ minHeight: 44, height: 'auto', '& .MuiChip-label': { whiteSpace: 'normal', py: 0.5 } }} data-toc={s.id}
          onClick={(e) => { e.preventDefault(); setOpen((o) => (o.includes(s.id) ? o : [...o, s.id])); window.history.replaceState(null, '', '#' + s.id); setTimeout(() => { const el = document.getElementById('guide-' + s.id); if (el) el.scrollIntoView({ block: 'start', behavior: 'smooth' }); }, 200); }} />)}
      </Stack>
    </Box>

    <Box>
      {sections.map((s) => {
        const expanded = open.includes(s.id);
        return <Accordion key={s.id} id={'guide-' + s.id} expanded={expanded} disableGutters data-section={s.id}
          onChange={(e, value) => setOpen((o) => (value ? [...o, s.id] : o.filter((x) => x !== s.id)))}
          slotProps={{ transition: { unmountOnExit: true } }} sx={{ scrollMarginTop: 80 }}>
          <AccordionSummary expandIcon={<DownOutlined />} sx={{ minHeight: 48 }}>
            <Typography variant="h5" component="h2" sx={{ overflowWrap: 'anywhere' }}><Inline items={s.title} facts={facts} /></Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ minWidth: 0 }}>
            <Stack spacing={1.5} sx={{ minWidth: 0 }}>
              <Box className="guide-no-print">
                <Button startIcon={<LinkOutlined />} onClick={() => copy(link(s.id), t('mgmt.guide.theLink', 'the link to this section'))} sx={{ minHeight: 44 }} data-section-link={s.id}>
                  {t('mgmt.guide.copyLink', 'Copy a link to this section')}
                </Button>
                <Typography variant="caption" component="div" color="textSecondary">{t('mgmt.guide.linkNote', 'It opens inside the panel, for people signed in who may see this app.')}</Typography>
              </Box>
              <Blocks blocks={s.blocks} facts={facts} />
            </Stack>
          </AccordionDetails>
        </Accordion>;
      })}
    </Box>
  </Stack>;
};

// Whether an app (its routes) has a guide.
export const hasGuide = (routes) => (routes || []).some((r) => r && r.ForkGuide && GUIDES[r.ForkGuide]);

export default AppGuide;
