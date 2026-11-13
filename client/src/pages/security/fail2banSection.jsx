// Fork addition: the Fail2ban status on the security dashboard.
//
// Mounted in pages/security/index.jsx between "Addresses" and "Incidents",
// inside a full-width Grid item. The panel itself (state in plain words, the
// banned addresses with "Unban", the jails and their settings) is
// pages/security/fail2ban.jsx; it needs nothing from the dashboard.
import Fail2banPanel from './fail2ban';

const Fail2banSection = () => <Fail2banPanel />;

export default Fail2banSection;
