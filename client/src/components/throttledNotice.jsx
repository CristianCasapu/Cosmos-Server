// Fork addition: what the app shows when its very first request was refused
// for "too many requests" (429) — words, a countdown and a way to try again,
// where upstream shows a red icon on an empty page. It is rendered before the
// theme exists, so it uses the skin's variables and the page's own font only.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { throttleSecondsLeft } from '../utils/sessionCheck';

// never reload sooner than this, however the countdown was started
const SHORTEST_WAIT = 20;

const ThrottledNotice = () => {
  const { t } = useTranslation();
  const [left, setLeft] = React.useState(() => Math.max(SHORTEST_WAIT, throttleSecondsLeft()));

  React.useEffect(() => {
    if (left <= 0) {
      window.location.reload();
      return undefined;
    }
    const timer = setTimeout(() => setLeft(left - 1), 1000);
    return () => clearTimeout(timer);
  }, [left]);

  return <div role="alert" style={{
    maxWidth: 460,
    margin: '0 16px',
    padding: '20px 24px',
    borderRadius: 12,
    border: '1px solid var(--ct-border)',
    background: 'var(--ct-surface)',
    boxShadow: 'var(--ct-shadow)',
    color: 'CanvasText',
    // the theme's font (config.jsx): the theme itself is not there yet
    fontFamily: "'Public Sans', sans-serif",
    lineHeight: 1.5,
  }}>
    <div style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 8 }}>
      {t('throttled.title', 'The server is holding this browser back for a moment')}
    </div>
    <div style={{ color: 'var(--ct-muted)', marginBottom: 12 }}>
      {t('throttled.text',
        'More requests came from your address within one minute than the server accepts, so it refuses new ones for a short while. Your applications keep running. This page tries again by itself in {{seconds}} s.',
        { seconds: Math.max(left, 0) })}
    </div>
    <button type="button" onClick={() => window.location.reload()} style={{
      font: 'inherit',
      fontWeight: 600,
      padding: '6px 16px',
      borderRadius: 8,
      cursor: 'pointer',
      background: 'transparent',
      color: 'var(--ct-accent)',
      border: '1px solid var(--ct-border-strong)',
    }}>
      {t('throttled.retry', 'Try again now')}
    </button>
  </div>;
};

export default ThrottledNotice;
