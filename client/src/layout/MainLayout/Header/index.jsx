import PropTypes from 'prop-types';

// material-ui
import { useTheme } from '@mui/material/styles';
import { AppBar, IconButton, Toolbar, useMediaQuery, Tooltip } from '@mui/material';
import { useTranslation } from 'react-i18next';

// project import
import AppBarStyled from './AppBarStyled';
import HeaderContent from './HeaderContent';

// assets
import { MenuFoldOutlined, MenuUnfoldOutlined } from '@ant-design/icons';

// ==============================|| MAIN LAYOUT - HEADER ||============================== //

const Header = ({ open, handleDrawerToggle }) => {
    const theme = useTheme();
    const { t } = useTranslation(); // Fork addition: tooltip on the menu toggle
    const matchDownMD = useMediaQuery(theme.breakpoints.down('lg'));

    const iconBackColor = theme.palette.mode === 'dark' ? 'grey.700' : 'grey.100';
    const iconBackColorOpen = theme.palette.mode === 'dark' ? 'grey.800' : 'grey.200';

    // common header
    const mainHeader = (
        <Toolbar>
            <Tooltip title={open ? t('tooltip.closeMenu', 'Close menu') : t('tooltip.openMenu', 'Open menu')}><IconButton
                disableRipple
                aria-label="open drawer"
                onClick={handleDrawerToggle}
                edge="start"
                color="red"
                sx={{ color: 'text.primary', bgcolor: open ? iconBackColorOpen : iconBackColor, ml: { xs: 0, lg: -2 } }}
            >
                {!open ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            </IconButton></Tooltip>
            <HeaderContent />
        </Toolbar>
    );

    // app-bar params
    const appBar = {
        position: 'fixed',
        color: 'inherit',
        elevation: 0,
        sx: {
            borderBottom: `1px solid ${theme.palette.divider}`
            // boxShadow: theme.customShadows.z1
        }
    };

    return (
        <>
            {!matchDownMD ? (
                <AppBarStyled open={open} {...appBar}>
                    {mainHeader}
                </AppBarStyled>
            ) : (
                <AppBar {...appBar}>{mainHeader}</AppBar>
            )}
        </>
    );
};

Header.propTypes = {
    open: PropTypes.bool,
    handleDrawerToggle: PropTypes.func
};

export default Header;
