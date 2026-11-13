// material-ui
import { Box, Typography } from '@mui/material';

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

    return <Box sx={{ pt: 2 }}>{navGroups}</Box>;
};

export default Navigation;
