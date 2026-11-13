// Fork addition: the gates every upstream screen uses to hide features that
// need a licence (see utils/paidVisibility.js for the rules). One status read
// per session, shared; read again after the settings are saved.
import * as React from 'react';
import { Alert } from '@mui/material';
import { useTranslation } from 'react-i18next';

import * as API from '../api';
import { CosmosCheckbox } from '../pages/config/users/formShortcuts';
import {
  UNKNOWN, stateFromStatus, hidePaid, showPurchase, offerMode, showPaidPart,
  filterMenu, filterTabs,
} from '../utils/paidVisibility';

let current = UNKNOWN;
let pending = null;
const listeners = new Set();

const publish = (state) => {
  current = state;
  listeners.forEach((notify) => notify(state));
};

function load() {
  if (pending) return pending;
  pending = API.getStatus().then((res) => {
    publish(stateFromStatus(res && res.data));
  }).catch(() => {
    // not known: behave as licensed, show no offer; try again on the next mount
    pending = null;
  });
  return pending;
}

// Read the status again (after the settings or the licence were saved).
export function refreshPaidVisibility() {
  pending = null;
  return load();
}

export function usePaidVisibility() {
  const [state, setState] = React.useState(current);
  React.useEffect(() => {
    listeners.add(setState);
    if (current !== state) setState(current);
    if (current === UNKNOWN) load();
    return () => { listeners.delete(setState); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return {
    state,
    hidePaid: hidePaid(state),
    showPurchase: showPurchase(state),
    offerMode: offerMode(state),
  };
}

export const useHidePaid = () => usePaidVisibility().hidePaid;

// The side menu with paid entries and the purchase entry resolved.
export function useForkMenu(groups) {
  const { state } = usePaidVisibility();
  return React.useMemo(() => filterMenu(groups, state), [groups, state]);
}

// Tabs marked `paid: true` removed when hidden.
export function useWithoutPaidTabs(tabs) {
  const { state } = usePaidVisibility();
  return filterTabs(tabs, state);
}

// What a page that needs a licence shows instead of an offer or of itself.
export const PaidHiddenNote = () => {
  const { t } = useTranslation();
  return <Alert severity="info">
    {t('fork.paid.note', 'This feature needs a licence.')}
  </Alert>;
};

// Wraps the sales page: as upstream, the neutral note, or nothing yet.
export function withPaidOfferGate(Offer) {
  const Gated = (props) => {
    const mode = usePaidVisibility().offerMode;
    if (mode === 'note') return <PaidHiddenNote />;
    if (mode === 'none') return null;
    return <Offer {...props} />;
  };
  Gated.displayName = 'PaidOfferGate';
  return Gated;
}

// A field or section that needs a licence; `keep` keeps it visible (a value
// that is on must stay where it can be turned off).
export const PaidOnly = ({ keep, children }) => {
  const { state } = usePaidVisibility();
  return showPaidPart(state, keep) ? children : null;
};

// A page that needs a licence, opened by its address.
export const PaidRoute = ({ children }) => {
  return useHidePaid() ? <PaidHiddenNote /> : children;
};

// The setting, in the licence card of Configuration > General. Checked means
// hidden; the form maps it to ShowPaidFeatures (stored inverted).
export const HidePaidSwitch = ({ formik }) => {
  const { t } = useTranslation();
  return <CosmosCheckbox
    name="HidePaidFeatures"
    formik={formik}
    label={t('fork.paid.switchLabel', 'Hide features that need a licence')}
    helperText={t('fork.paid.switchHelp', 'Without a valid licence, menu entries, pages, fields and offers for paid features are not shown. With a licence nothing is hidden.')}
  />;
};
