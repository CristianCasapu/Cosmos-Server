// material-ui
import { Box, Typography } from '@mui/material';
import { useEffect } from 'react';
import { useDispatch } from 'react-redux';
import { useLocation } from 'react-router-dom';
import { activeItem } from '../../../../../store/reducers/menu';
import { menuEntries, menuEntryFor } from '../../../../../utils/wayfinding'; // Fork addition: mark the current page

// project import
import NavGroup from './NavGroup';
import menuItem from '../../../../../menu-items';
import { useIsPro } from '../../../../../utils/pro';
import { useForkMenu } from '../../../../../components/paidVisibility'; // Fork addition

// ==============================|| DRAWER CONTENT - NAVIGATION ||============================== //

const Navigation = () => {
    // Renames entries Pro overrides via `titlePro`; the group shape is set in menu-items/index.jsx.
    const isPro = useIsPro() === true;

    const navGroups = useForkMenu(menuItem.items) // Fork addition: hides entries that need a licence
        .map((item) => ({
            ...item,
            children: (item.children || []).map((child) =>
                (isPro && child.titlePro ? { ...child, title: child.titlePro } : child)),
        }))
        .map((item) => {
            switch (item.type) {
                case 'group':
                    return <NavGroup key={item.id} item={item} />;
                default:
                    return (
                        <Typography key={item.id} variant="h6" color="error" align="center">
                            Fix - Navigation Group
                        </Typography>
                    );
            }
        });

    // Fork addition: the entry of the current page is marked on every
    // navigation. Upstream marked it only on a click, and after a page load
    // only when the entry's id was a part of the address: Home, Monitoring,
    // Market, URLs, Users, OpenID and Configuration never were.
    const location = useLocation();
    const dispatch = useDispatch();
    useEffect(() => {
        const entry = menuEntryFor(location.pathname, menuEntries(menuItem));
        dispatch(activeItem({ openItem: entry ? [entry.id] : [] }));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [location.pathname]);

    return <Box sx={{ pt: 2 }}>{navGroups}</Box>;
};

export default Navigation;
