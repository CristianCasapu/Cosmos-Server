// Fork addition: a value the form proposes, shown with where it came from.
// The rule (utils/proposals.js): a proposal fills empty fields by itself and
// never replaces what the administrator entered; when the form holds
// something else, the proposal waits here with a button to take it.
import * as React from 'react';
import { Alert, Button, Stack, Typography } from '@mui/material';

// title: what is proposed, in words. source: where it comes from. status:
// what is known about it right now. `onUse` shows the button; `inUse` says
// the form already holds it.
const ProposalBox = ({ severity = 'info', title, source, status, inUse, inUseText, heldText, onUse, useLabel, children }) => (
  <Alert severity={severity} sx={{ '& .MuiAlert-message': { width: '100%', minWidth: 0 } }}>
    <Stack spacing={0.75} alignItems="flex-start" sx={{ overflowWrap: 'anywhere' }}>
      <Typography variant="subtitle1" component="div" sx={{ fontWeight: 600 }}>{title}</Typography>
      {source && <Typography variant="body2" component="div">{source}</Typography>}
      {status && <Typography variant="body2" component="div">{status}</Typography>}
      {children}
      {inUse && inUseText && <Typography variant="body2" component="div">{inUseText}</Typography>}
      {!inUse && heldText && <Typography variant="body2" component="div">{heldText}</Typography>}
      {!inUse && onUse && <Button size="small" variant="outlined" color="inherit" onClick={onUse}>{useLabel}</Button>}
    </Stack>
  </Alert>
);

export default ProposalBox;
