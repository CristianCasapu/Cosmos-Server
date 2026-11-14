// Fork addition: the steps of a check, as the server went through them: a
// mark and plain words for each, and under the step that failed what to do
// next, with the other side's own words as a detail. For "verify before
// save": settings whose mistakes only show later (mail, a remote disk, a
// token) are tried from the form, and the form says where the attempt
// stopped.
import * as React from 'react';
import { Stack, Typography } from '@mui/material';
import { CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';

// steps: [{ name, ok, detail }]. label(step) and advice(step) return text;
// advice is asked for failed steps only. Everything is rendered as text.
const StepList = ({ steps, label, advice, detailLabel }) => (
  <Stack spacing={0.75} sx={{ width: '100%' }}>
    {(steps || []).map((step, index) => {
      const next = !step.ok && advice ? advice(step) : '';
      return <Stack key={step.name + index} spacing={0.5}>
        <Stack direction="row" spacing={1} alignItems="baseline">
          <span aria-hidden="true">{step.ok ? <CheckCircleOutlined /> : <CloseCircleOutlined />}</span>
          <Typography variant="body2" component="span" sx={{ fontWeight: step.ok ? 400 : 600, overflowWrap: 'anywhere' }}>
            {label(step)}
          </Typography>
        </Stack>
        {next && <Typography variant="body2" sx={{ pl: 3, overflowWrap: 'anywhere' }}>{next}</Typography>}
        {!step.ok && step.detail && <Typography variant="caption" color="text.secondary" sx={{ pl: 3, overflowWrap: 'anywhere' }}>
          {detailLabel ? detailLabel + ' ' : ''}{step.detail}
        </Typography>}
      </Stack>;
    })}
  </Stack>
);

export default StepList;
