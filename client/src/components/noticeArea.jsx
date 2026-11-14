// Fork addition: the one pattern for "things that need attention" on a page
// (ServApps, a stack's page, the home page): one surface, one line per item
// with its action, the most urgent first, folded after two lines. The order
// and the folding are in utils/notices.js.
//
// items: [{ id, severity: 'error' | 'warning' | 'unknown' | 'info' | 'success',
//           text (node), detail (node, optional: a second, quieter sentence),
//           actions (node, optional), attrs (data-* attributes for the line) }]
import * as React from 'react';
import { Button, Typography } from '@mui/material';
import {
  CheckCircleOutlined, CloseCircleOutlined, DownOutlined, ExclamationCircleOutlined,
  InfoCircleOutlined, QuestionCircleOutlined, UpOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { foldNotices, hiddenSummary, worstSeverity, DEFAULT_AFTER } from '../utils/notices';

const ICONS = {
  error: CloseCircleOutlined,
  warning: ExclamationCircleOutlined,
  unknown: QuestionCircleOutlined,
  info: InfoCircleOutlined,
  success: CheckCircleOutlined,
};

export const NoticeIcon = ({ severity }) => {
  const Icon = ICONS[severity] || ICONS.info;
  return <Icon className={'ct-notice-icon ct-notice-' + (ICONS[severity] ? severity : 'info')} aria-hidden="true" />;
};

const NoticeArea = ({ items, label, after = DEFAULT_AFTER, className, ...rest }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const listId = React.useId();
  const { shown, hidden, foldable } = foldNotices(items, open, after);
  if (!shown.length) return null;

  const severityWord = {
    error: t('notices.severity.error', 'Problem:'),
    warning: t('notices.severity.warning', 'Warning:'),
    unknown: t('notices.severity.unknown', 'Not checked:'),
    info: t('notices.severity.info', 'Note:'),
    success: t('notices.severity.success', 'Done:'),
  };
  const more = hiddenSummary(hidden).map(({ severity, count }) => ({
    error: t('notices.more.error', '{{count}} problem(s)', { count }),
    warning: t('notices.more.warning', '{{count}} warning(s)', { count }),
    unknown: t('notices.more.unknown', '{{count}} not checked', { count }),
    info: t('notices.more.info', '{{count}} note(s)', { count }),
    success: t('notices.more.success', '{{count}} done', { count }),
  }[severity])).join(', ');

  return <section
    className={'ct-notices ct-notices-' + (worstSeverity(items) || 'info') + (className ? ' ' + className : '')}
    aria-label={label || t('notices.label', 'Needs attention')}
    data-notices=""
    {...rest}
  >
    <ul className="ct-notices-list" id={listId}>
      {shown.map((item) => <li key={item.id} className="ct-notice" data-notice={item.id} {...(item.attrs || {})}>
        <NoticeIcon severity={item.severity} />
        <div className="ct-notice-body">
          <Typography variant="body2" component="div" className="ct-notice-text">
            <span className="ct-visually-hidden">{severityWord[item.severity] || severityWord.info} </span>
            {item.text}
          </Typography>
          {item.detail && <Typography variant="body2" component="div" className="ct-notice-detail">{item.detail}</Typography>}
        </div>
        {item.actions && <div className="ct-notice-actions">{item.actions}</div>}
      </li>)}
    </ul>
    {foldable && <Button
      size="small"
      className="ct-notices-toggle"
      aria-expanded={open ? 'true' : 'false'}
      aria-controls={listId}
      startIcon={open ? <UpOutlined /> : <DownOutlined />}
      onClick={() => setOpen(!open)}
    >
      {open
        ? t('notices.less', 'Show less')
        : t('notices.showMore', 'Show {{count}} more ({{what}})', { count: hidden.length, what: more })}
    </Button>}
  </section>;
};

export default NoticeArea;
