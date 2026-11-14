// Fork addition: the menu entry of the audit page (pages/securityAudit). Shown
// only to whoever may read the server's logs, which the page's routes need.
import { AuditOutlined } from '@ant-design/icons';
import { PERM_ADMIN_READ } from '../utils/permissions';

const securityAudit = {
    id: 'security-audit',
    title: 'menu-items.management.securityAudit',
    type: 'item',
    url: '/cosmos-ui/security-audit',
    icon: AuditOutlined,
    permission: PERM_ADMIN_READ
};

export default securityAudit;
