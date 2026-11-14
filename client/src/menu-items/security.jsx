// Fork addition: the "Security" menu group. The security dashboard is its
// first item, the user audit panel its second.
import { SafetyCertificateOutlined } from '@ant-design/icons';
import { PERM_ADMIN_READ } from '../utils/permissions';
import securityAudit from './securityAudit';

const security = {
    id: 'group-security',
    title: 'menu-items.security',
    type: 'group',
    children: [
        {
            id: 'security',
            title: 'menu-items.security.dashboard',
            type: 'item',
            url: '/cosmos-ui/security',
            icon: SafetyCertificateOutlined,
            breadcrumbs: false,
            permission: PERM_ADMIN_READ
        },
        securityAudit,
    ],
};

export default security;
