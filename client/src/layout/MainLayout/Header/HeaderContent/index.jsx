import React from 'react';

// material-ui
import { Box, Chip, IconButton, Link, Stack, useMediaQuery } from '@mui/material';
import { GithubOutlined } from '@ant-design/icons';

// project import
import Search from './Search';
import Profile from './Profile';
import Notification from './Notification';
import MobileSection from './MobileSection';
import Jobs from './jobs';
import { useTranslation } from 'react-i18next';
import RestartMenu from './restartMenu';
import TerminalHeader from './terminal';
import SudoModal from './sudoModal';
import ModeToggle from '../../../../components/modeToggle'; // Fork addition: the skin's light/dark switch
import { PWAInstallButton } from '../../../../components/pwa'; // Fork addition: install the panel as an app

// ==============================|| HEADER - CONTENT ||============================== //

const HeaderContent = () => {
    const { t } = useTranslation();
    const matchesXs = useMediaQuery((theme) => theme.breakpoints.down('md'));

    return (
        <>
            {!matchesXs && <Search />}
            {matchesXs && <Box sx={{ width: '100%', ml: 1 }} />}

            <Stack direction="row" spacing={2}>
                <TerminalHeader />
                <Jobs />
                <Notification />
                <ModeToggle sx={{ display: { xs: 'none', sm: 'inline-flex' } }} />{/* Fork addition: on phones it is in the menu's footer */}
                <PWAInstallButton sx={{ display: { xs: 'none', sm: 'inline-flex' } }} />{/* Fork addition: only while the browser can install it; on phones in the menu's footer */}

                <SudoModal />
                <Link href="/cosmos-ui/logout" underline="none">
                    <Chip label={t('global.logout')} style={{height: '36px'}} />
                </Link>
                <RestartMenu />
            </Stack>
            {/* {!matchesXs && <Profile />}
            {matchesXs && <MobileSection />} */}
        </>
    );
};

export default HeaderContent;
