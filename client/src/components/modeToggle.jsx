// Fork addition: the light/dark switch of the skin as a button of the panel
// (in the header, and in the menu's footer on phones). The skin's own script
// (client/public/skin/mode.js) keeps the choice and reloads the page; without
// the skin there is nothing to switch and nothing is shown.
import * as React from 'react';
import { IconButton, Tooltip } from '@mui/material';
import { useTranslation } from 'react-i18next';

// the same two pictures as the skin's own button: a moon while dark, a sun while light
const Picture = ({ dark }) => (
  <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ fontSize: '1.15em' }}>
    {dark
      ? <path d="M20.5 14.2A8.5 8.5 0 1 1 9.8 3.5a6.8 6.8 0 0 0 10.7 10.7z" />
      : <><circle cx="12" cy="12" r="4.2" /><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M5.3 18.7l1.6-1.6M17.1 6.9l1.6-1.6" /></>}
  </svg>
);

const ModeToggle = ({ sx, size }) => {
  const { t } = useTranslation();
  const skin = typeof window !== 'undefined' ? window.ctMode : null;
  if (!skin || typeof skin.choose !== 'function') return null;

  const dark = skin.resolved === 'dark';
  const label = dark
    ? t('skin.mode.toLight', 'Switch to the light theme')
    : t('skin.mode.toDark', 'Switch to the dark theme');
  const auto = skin.mode === 'auto';
  const hint = auto
    ? t('skin.mode.followsSystem', 'Now: following the device')
    : t('skin.mode.backToSystem', 'Right click: follow the device again');

  return (
    <Tooltip title={<span>{label}<br />{hint}</span>}>
      <IconButton
        disableRipple
        color="secondary"
        size={size}
        className="ct-mode-button"
        aria-label={label}
        sx={{ color: 'text.primary', ...(sx || {}) }}
        onClick={() => skin.choose(dark ? 'light' : 'dark')}
        onContextMenu={(event) => {
          event.preventDefault();
          if (!auto) skin.choose('auto');
        }}
      >
        <Picture dark={dark} />
      </IconButton>
    </Tooltip>
  );
};

export default ModeToggle;
