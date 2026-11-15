// Fork addition: why a page of settings cannot be changed by the reader.
//   'none'      they may change it
//   'password'  their role allows it, but the password confirmation is
//               missing or has run out (utils/sudoPrompt.js)
//   'role'      their role does not allow it
export const readOnlyReason = (mayNow, roleAllows) => {
  if (mayNow) return 'none';
  return roleAllows ? 'password' : 'role';
};
