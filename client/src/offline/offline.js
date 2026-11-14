// Fork addition: the offline page's behaviour (client/offline.html). It tries
// the server again by itself (when the device comes back online, and on a
// schedule) and on "Try again"; once Cosmos answers, it loads the page that
// was asked for. It never shows anything it remembers of the server.
import './offline.css';
import { retryDelay, serverBack } from '../utils/pwaClient';

const button = document.getElementById('ct-offline-retry');
const status = document.getElementById('ct-offline-status');
let attempt = 0;
let timer = null;
let busy = false;

const clock = () => new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });

const say = (text) => { status.textContent = text; };

const schedule = () => {
  clearTimeout(timer);
  const seconds = retryDelay(attempt);
  say(`No answer at ${clock()}. Trying again in ${seconds} seconds.`);
  timer = setTimeout(check, seconds * 1000);
};

// A plain request for the panel's start page: not a page navigation, so the
// service worker leaves it to the network. Nothing is kept from its answer.
async function check() {
  if (busy) return;
  busy = true;
  clearTimeout(timer);
  button.disabled = true;
  say('Trying to reach the server…');
  let back = false;
  try {
    const response = await fetch('/cosmos-ui/', { method: 'HEAD', cache: 'no-store', credentials: 'same-origin', redirect: 'manual' });
    // a redirect (opaque here) is an answer of the server too
    back = response.type === 'opaqueredirect' || serverBack(response.status);
  } catch (error) {
    back = false;
  }
  busy = false;
  button.disabled = false;
  if (back) {
    say('The server answers. Opening the panel…');
    window.location.reload();
    return;
  }
  attempt += 1;
  schedule();
}

button.addEventListener('click', () => { attempt = 0; check(); });
window.addEventListener('online', () => { attempt = 0; check(); });
schedule();
