import PropTypes from 'prop-types';
import { useMemo } from 'react';

// material-ui
import { useTheme } from '@mui/material/styles';
import { Box, Chip, Drawer, useMediaQuery } from '@mui/material';

// project import
import DrawerHeader from './DrawerHeader';
import DrawerContent from './DrawerContent';
import MiniDrawerStyled from './MiniDrawerStyled';
import { drawerWidth } from '../../../config';
import NavCard from './DrawerContent/NavCard';

import {version} from '../../../../../package.json';
import { LanguagesSelect } from './languages';
import ModeToggle from '../../../components/modeToggle'; // Fork addition: the skin's light/dark switch
import { PWAInstallButton } from '../../../components/pwa'; // Fork addition: install the panel as an app

// Fork addition: the fork's build number, set when a release is built. The
// version above stays upstream's, which is what app requirements refer to.
const forkBuild = import.meta.env.VITE_FORK_BUILD ? '-fork.' + import.meta.env.VITE_FORK_BUILD : '';

// ==============================|| MAIN LAYOUT - DRAWER ||============================== //

const MainDrawer = ({ open, handleDrawerToggle, window }) => {
    const theme = useTheme();
    const matchDownMD = useMediaQuery(theme.breakpoints.down('lg'));

    // responsive drawer container
    const container = window !== undefined ? () => window().document.body : undefined;

    // header content
    const drawerContent = useMemo(() => <DrawerContent />, []);
    const drawerHeader = useMemo(() => <DrawerHeader open={open} />, [open]);
    const footer = <div className="ct-drawer-footer" style={{ margin: '10px', display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
        <ModeToggle size="small" sx={{ display: { xs: 'inline-flex', sm: 'none' }, mr: '10px' }} />{/* Fork addition: from 600 px up it is in the header */}
        <PWAInstallButton size="small" sx={{ display: { xs: 'inline-flex', sm: 'none' }, mr: '10px' }} />{/* Fork addition: from 600 px up it is in the header */}
        <LanguagesSelect />
        <Chip
            label={version.replace('unstable', '') + forkBuild}
            sx={{ marginLeft: '10px', height: 24, '& .MuiChip-label': { fontSize: '0.7rem', py: 0.25 } }}
            component="a"
            href="/"
            clickable
        />
    </div>;

    return (
        // Fork addition: the class tells the skin whether the menu is open: closed, it is taken out of sight and out of the keyboard's way
        <Box component="nav" className={open ? 'ct-drawer ct-drawer-open' : 'ct-drawer ct-drawer-closed'} sx={{ flexShrink: { md: 0 }, zIndex: 1300 }} aria-label="mailbox folders">
            {!matchDownMD ? (
                <MiniDrawerStyled
                    variant="permanent" open={open}>
                    {drawerHeader}
                    {drawerContent}
                    {footer}
                </MiniDrawerStyled>
            ) : (
                <Drawer
                    container={container}
                    variant="temporary"
                    open={open}
                    onClose={handleDrawerToggle}
                    ModalProps={{ keepMounted: true }}
                    sx={{
                        display: { xs: 'block', lg: 'none' },
                        '& .MuiDrawer-paper': {
                            boxSizing: 'border-box',
                            width: drawerWidth,
                            borderRight: `1px solid ${theme.palette.divider}`,
                            backgroundImage: 'none',
                            boxShadow: 'inherit'
                        }
                    }}
                >
                    {open && drawerHeader}
                    {open && drawerContent}
                    {open && footer}
                </Drawer>
            )}
        </Box>
    );
};

MainDrawer.propTypes = {
    open: PropTypes.bool,
    handleDrawerToggle: PropTypes.func,
    window: PropTypes.object
};

export default MainDrawer;
