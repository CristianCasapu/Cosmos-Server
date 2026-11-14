// Fork addition: the security dashboard's timeline, incidents per group per
// bar, styled like the Monitoring charts (dashboard/components/plot.jsx).
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Box, Typography } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import Chart from 'react-apexcharts';

import MainCard from '../../components/MainCard';
import { bucketLabel, groupLabel, timelineSeries } from '../../utils/securityDashboard';

const groupColour = (theme, id) => ({
  login: theme.palette.error.main,
  lockout: theme.palette.warning.main,
  ban: theme.palette.secondary.main,
  scan: theme.palette.primary.main,
  access: theme.palette.info.main,
  other: theme.palette.text.disabled,
}[id] || theme.palette.text.secondary);

const Timeline = ({ overview }) => {
  const { t, i18n } = useTranslation();
  const theme = useTheme();
  const { secondary } = theme.palette.text;
  const line = theme.palette.divider;

  const buckets = (overview && overview.buckets) || [];
  const bucketSeconds = (overview && overview.bucketSeconds) || 3600;
  const series = useMemo(() => timelineSeries(buckets), [buckets]);
  const categories = useMemo(() => buckets.map((b) => bucketLabel(b.start, bucketSeconds, { locale: i18n.language })), [buckets, bucketSeconds, i18n.language]);

  const options = {
    chart: {
      type: 'bar',
      stacked: true,
      toolbar: { show: false },
      zoom: { enabled: false },
      animations: { enabled: false },
    },
    plotOptions: { bar: { columnWidth: '70%' } },
    dataLabels: { enabled: false },
    colors: series.map((s) => groupColour(theme, s.id)),
    xaxis: {
      categories,
      labels: { style: { colors: secondary, fontSize: '12px' }, hideOverlappingLabels: true, rotate: 0 },
      tickAmount: Math.min(categories.length, 8),
      axisBorder: { color: line },
      axisTicks: { color: line },
    },
    yaxis: {
      labels: { style: { colors: [secondary], fontSize: '12px' }, formatter: (v) => Math.round(v) },
      forceNiceScale: true,
      min: 0,
    },
    grid: { borderColor: line },
    legend: { labels: { colors: secondary } },
    tooltip: { theme: theme.palette.mode },
  };

  return <>
    <Typography variant="h5">{t('security.timeline.title', 'Incidents over time')}</Typography>
    <MainCard content={false} sx={{ mt: 1.5 }}>
      {series.length === 0 ? (
        <Box sx={{ p: 3, textAlign: 'center' }}>
          <Typography color="textSecondary">{t('security.timeline.empty', 'No incidents in this period.')}</Typography>
        </Box>
      ) : (
        <Box sx={{ pt: 1, pr: 2 }}>
          <Chart
            options={options}
            series={series.map((s) => ({ name: t(...groupLabel(s.id)), data: s.data }))}
            type="bar"
            height={300}
          />
        </Box>
      )}
    </MainCard>
  </>;
};

export default Timeline;
