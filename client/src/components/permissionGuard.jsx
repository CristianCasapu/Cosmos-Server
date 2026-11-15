import React from "react";
import { Tooltip } from "@mui/material";
import { useClientInfos } from "../utils/hooks";
import { useTranslation } from "react-i18next";
import { askForPassword } from "../utils/sudoPrompt";

// Fork addition: without the permission, take the handlers off the wrapped
// control and off anything nested in it. Upstream only cleared the direct
// child, so a button inside a Tooltip could still be triggered from the
// keyboard.
const disarm = (el) => {
  if (!React.isValidElement(el)) return el;
  // a Fragment takes no handlers (React 19 says so), and the children go back
  // as separate arguments so that they need no keys
  const props = el.type === React.Fragment ? {} : { onClick: undefined, onChange: undefined };
  const kids = el.props.children;
  if (kids === undefined || kids === null) return React.cloneElement(el, props);
  return Array.isArray(kids) ? React.cloneElement(el, props, ...kids.map(disarm)) : React.cloneElement(el, props, disarm(kids));
};

const PermissionGuard = ({ children, permission, alwaysShow }) => {
  const { t } = useTranslation();
  const { hasPermission, hasRolePermission } = useClientInfos();
  const hasPerm = hasPermission(permission);
  const hasRole = hasRolePermission(permission);
  // Fork addition: the tooltip closes when the click opens the password
  // prompt and stays closed until the pointer leaves: a tooltip left open (a
  // tap on a phone opens it with the click, after its delay) lies above the
  // prompt and can cover its Submit button.
  const [tipOpen, setTipOpen] = React.useState(false);
  const tipHeld = React.useRef(false);

  if (!hasRole && !alwaysShow) return null;
  if (hasPerm) return children;

  // Fork addition: when only the password confirmation is missing (the role
  // has the permission), a click asks for the password right here instead of
  // sending the admin to the button at the top; see utils/sudoPrompt.js.
  if (hasRole) {
    return (
      <Tooltip title={t('sudo.requiredClick', 'Confirm your password to use this function: click here')} open={tipOpen} onOpen={() => { if (!tipHeld.current) setTipOpen(true); }} onClose={() => setTipOpen(false)}>
        <span style={{ cursor: 'pointer' }} onMouseLeave={() => { tipHeld.current = false; }} onClick={(event) => { event.preventDefault(); event.stopPropagation(); tipHeld.current = true; setTipOpen(false); askForPassword('guard'); }}>
          <span style={{ pointerEvents: 'none', filter: 'grayscale(0.5)', opacity: 0.65 }}>
            {disarm(children)}
          </span>
        </span>
      </Tooltip>
    );
  }

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
