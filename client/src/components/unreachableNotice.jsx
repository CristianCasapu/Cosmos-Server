// Fork addition: what the panel shows when the server gave no answer at all
// while the page was starting (upstream: a big red icon without a word). Says
// what it means, tries again by itself every 15 seconds and on "Try again".
// Rendered before the theme exists: the skin's variables and the page's font
// only (the same look as throttledNotice.jsx and the installed app's offline page).
import * as React from 'react';
import { useTranslation } from 'react-i18next';

const EVERY = 15; // seconds between two tries

export const noticeStyle = {
  maxWidth: 460,
  margin: '0 16px',
  padding: '20px 24px',
  borderRadius: 'var(--ct-radius-l, 14px)',
  border: '1px solid var(--ct-border)',
  background: 'var(--ct-surface)',
  boxShadow: 'var(--ct-shadow)',
  color: 'var(--ct-text, CanvasText)',
  fontFamily: "'Public Sans', sans-serif",
  lineHeight: 1.5,
};

export const noticeButton = {
  font: 'inherit',
  fontWeight: 600,
  minHeight: 44,
  padding: '6px 16px',
  borderRadius: 'var(--ct-radius-m, 10px)',
  cursor: 'pointer',
  background: 'transparent',
  color: 'var(--ct-accent-text, var(--ct-accent))',
  border: '1px solid var(--ct-border-strong)',
};

const UnreachableNotice = () => {
  const { t } = useTranslation();
  const [left, setLeft] = React.useState(EVERY);

  React.useEffect(() => {
    if (left <= 0) {
      window.location.reload();
      return undefined;
    }
    const timer = setTimeout(() => setLeft(left - 1), 1000);
    return () => clearTimeout(timer);
  }, [left]);

  return <main role="alert" style={noticeStyle} data-unreachable="">
    <h1 style={{ fontSize: '1.1rem', fontWeight: 600, margin: '0 0 8px' }}>
      {t('unreachable.title', 'Cosmos cannot be reached')}
    </h1>
    <p style={{ margin: '0 0 8px' }}>
      {t('unreachable.text', 'This device got no answer from the server. Check this device\'s connection first; if it works, the server may be restarting, switched off, or not reachable from the network you are on.')}
    </p>
    <p style={{ color: 'var(--ct-muted)', margin: '0 0 12px' }}>
      {t('unreachable.again', 'This page tries again by itself in {{seconds}} s.', { seconds: Math.max(left, 0) })}
    </p>
    <button type="button" onClick={() => window.location.reload()} style={noticeButton}>
      {t('unreachable.retry', 'Try again')}
    </button>
  </main>;
};

export default UnreachableNotice;
