// Fork addition: the place of the Fail2ban status on the security dashboard.
//
// The Fail2ban integration (a separate piece of work) replaces this
// component with its own: same file, same default export, no props needed
// (it may take `period`, the dashboard's period: '24h', '7d' or '30d').
// It is mounted in pages/security/index.jsx between "Addresses" and
// "Incidents", inside a full-width Grid item. Until then it renders
// nothing, so the dashboard shows no empty box.
const Fail2banSection = () => null;

export default Fail2banSection;
