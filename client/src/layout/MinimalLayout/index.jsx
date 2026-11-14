import { Outlet } from 'react-router-dom';
import PageTitle from '../../components/pageTitle'; // Fork addition: the browser tab names the page

// ==============================|| MINIMAL LAYOUT ||============================== //

const MinimalLayout = () => (
    <>
        <PageTitle />{/* Fork addition */}
        <Outlet />
    </>
);

export default MinimalLayout;
