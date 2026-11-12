import React from "react";
import { Tooltip } from "@mui/material";
import { useClientInfos } from "../utils/hooks";
import { useTranslation } from "react-i18next";

// Fork addition: without the permission, take the handlers off the wrapped
// control and off anything nested in it. Upstream only cleared the direct
// child, so a button inside a Tooltip could still be triggered from the
// keyboard.
const disarm = (el) => {
  if (!React.isValidElement(el)) return el;
  const props = { onClick: undefined, onChange: undefined };
  const kids = el.props.children;
  if (kids === undefined || kids === null) return React.cloneElement(el, props);
  return React.cloneElement(el, props, Array.isArray(kids) ? kids.map(disarm) : disarm(kids));
};

const PermissionGuard = ({ children, permission, alwaysShow }) => {
  const { t } = useTranslation();
  const { hasPermission, hasRolePermission } = useClientInfos();
  const hasPerm = hasPermission(permission);
  const hasRole = hasRolePermission(permission);

  if (!hasRole && !alwaysShow) return null;
  if (hasPerm) return children;

  return (
    <Tooltip title={t('sudo.required')}>
      <span style={{ cursor: 'not-allowed' }}>
        <span style={{ pointerEvents: 'none', filter: 'grayscale(0.5)', opacity: 0.65 }}>
          {disarm(children)}
        </span>
      </span>
    </Tooltip>
  );
};

export default PermissionGuard;
