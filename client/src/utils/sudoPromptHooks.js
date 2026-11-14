// Fork addition: the React side of utils/sudoPrompt.js.
import { useEffect, useSyncExternalStore } from 'react';
import { getSudoState, noteInput, noteSession, subscribeSudo } from './sudoPrompt';

// Anything typed or chosen on the page counts as work that a reload would
// lose; the password prompt itself does not.
if (typeof document !== 'undefined') {
  const typed = (event) => {
    const target = event.target;
    if (target && target.closest && target.closest('[data-sudo-prompt]')) return;
    noteInput();
  };
  document.addEventListener('input', typed, true);
  document.addEventListener('change', typed, true);
}

// useSudoClock is the hook of useClientInfos: the component is drawn again
// at the moment the password confirmation ends, and when it was renewed
// without the page being loaded again.
export function useSudoClock(cookie) {
  useSyncExternalStore(subscribeSudo, () => getSudoState().tick);
  useEffect(() => { noteSession(cookie); }, [cookie]);
}

// useSudoRequest is the hook of the password prompt: the request to open it,
// or null.
export function useSudoRequest() {
  return useSyncExternalStore(subscribeSudo, () => getSudoState().request);
}
