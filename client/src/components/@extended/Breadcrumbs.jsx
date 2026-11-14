import PropTypes from 'prop-types';
import { Link, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

// material-ui
import MuiBreadcrumbs from '@mui/material/Breadcrumbs';
import { Box, Typography } from '@mui/material';

// project imports
import { crumbsFor, menuEntries } from '../../utils/wayfinding';

// ==============================|| BREADCRUMBS ||============================== //

// Fork addition: the crumbs come from utils/wayfinding.js. Upstream showed the
// raw parts of the address ("containers", "general"), an untranslated "Home"
// and no link back, inside a card; now: Home, the menu entry (a link when the
// page is below it) and the thing the page is about, on the page itself.
const Breadcrumbs = ({ navigation }) => {
    const { t } = useTranslation();
    const location = useLocation();
    const crumbs = crumbsFor(location.pathname, menuEntries(navigation));

    if (!crumbs.length) return <Typography />;

    return (
        <Box className="ct-breadcrumbs" sx={{ mb: { xs: 2, sm: 2.5 } }}>
            <MuiBreadcrumbs aria-label={t('navigation.breadcrumb', 'You are here')}>
                {crumbs.map((crumb, index) => {
                    const text = crumb.key ? t(crumb.key, crumb.text) : crumb.text;
                    const last = index === crumbs.length - 1;
                    return crumb.to && !last ? (
                        <Typography key={index} component={Link} to={crumb.to} variant="h6" color="textSecondary" className="ct-crumb-link" sx={{ textDecoration: 'none' }}>
                            {text}
                        </Typography>
                    ) : (
                        <Typography key={index} variant="subtitle1" color="textPrimary" aria-current={last ? 'page' : undefined} sx={{ overflowWrap: 'anywhere' }}>
                            {text}
                        </Typography>
                    );
                })}
            </MuiBreadcrumbs>
        </Box>
    );
};

Breadcrumbs.propTypes = {
    navigation: PropTypes.object,
    title: PropTypes.bool
};

export default Breadcrumbs;
