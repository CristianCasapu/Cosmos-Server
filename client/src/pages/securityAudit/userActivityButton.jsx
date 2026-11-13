// Fork addition: the "Activity" action of a row of the Users page: that
// user's activity and summary on the audit page. Shown only to whoever may
// read the record.
import * as React from 'react';
import { IconButton, Tooltip } from '@mui/material';
import { AuditOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useClientInfos } from '../../utils/hooks';
import { PERM_ADMIN_READ } from '../../utils/permissions';
import { userLink } from '../../utils/auditFilters';

const UserActivityButton = ({ nickname }) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const { hasPermission } = useClientInfos();
    if (!hasPermission(PERM_ADMIN_READ) || !nickname) return null;
    return <Tooltip title={t('securityAudit.userAction', 'Activity: what {{user}} did and what happened to the account', { user: nickname })}>
        <IconButton aria-label={t('securityAudit.userActionShort', 'Activity')} onClick={(e) => {
            e.stopPropagation();
            navigate(userLink(nickname));
        }}>
            <AuditOutlined />
        </IconButton>
    </Tooltip>;
};

export default UserActivityButton;
