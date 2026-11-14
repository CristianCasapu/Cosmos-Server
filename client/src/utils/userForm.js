// Fork addition: what the user dialogs check before they ask the server, and
// what they say will happen next. The rules are the server's own
// (src/user/create.go: a nickname of 3 to 32 letters and digits, an optional
// email address), so that the admin reads them next to the field instead of
// as a validator's message after the click. Nothing here imports the UI.

// Why a nickname cannot be used, or null:
//   'required' | 'short' | 'long' | 'chars' | 'taken'
export const nicknameProblem = (nickname, existing) => {
  const name = String(nickname || '').trim();
  if (!name) {
    return 'required';
  }
  if (!/^[a-zA-Z0-9]+$/.test(name)) {
    return 'chars';
  }
  if (name.length < 3) {
    return 'short';
  }
  if (name.length > 32) {
    return 'long';
  }
  // the server keeps nicknames in lower case
  if ((existing || []).some((other) => String(other || '').toLowerCase() === name.toLowerCase())) {
    return 'taken';
  }
  return null;
};

// 'invalid' for something that is not an email address, null for one or for nothing.
export const emailProblem = (email) => {
  const text = String(email || '').trim();
  if (!text) {
    return null;
  }
  return /^[^\s@]+@[^\s@]+\.[^\s@.]+$/.test(text) && text.length <= 254 ? null : 'invalid';
};

// How the new user gets the link to set a password:
//   'mail'          Cosmos sends it to the address given
//   'linkNoAddress' no address: the admin passes the link on
//   'linkNoMail'    an address, but Cosmos cannot send mail: the admin passes the link on
export const inviteWay = ({ email, canSendEmail }) => {
  if (!String(email || '').trim()) {
    return 'linkNoAddress';
  }
  return canSendEmail ? 'mail' : 'linkNoMail';
};

// The built-in roles, in the order the dialogs show them. Roles of the
// server's own (groups) keep their name and get no description.
export const BUILT_IN_ROLES = { 0: 'guest', 1: 'user', 2: 'admin' };

// The roles as choices: [{ value, name, kind }], the usual one (User) first.
export const roleChoices = (roles) => Object.entries(roles || {})
  .map(([id, name]) => ({ value: Number(id), name: String(name), kind: BUILT_IN_ROLES[id] || 'other' }))
  .sort((a, b) => (a.value === 1 ? -1 : b.value === 1 ? 1 : a.value - b.value));
