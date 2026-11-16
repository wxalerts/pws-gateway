import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { App } from './App';
import { AuthProvider } from './auth/AuthContext';
import { UnitsProvider } from './lib/UnitsContext';
import './styles.css';

const root = document.getElementById('root');
if (!root) throw new Error('missing #root');

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <UnitsProvider>
        <AuthProvider>
          <App />
        </AuthProvider>
      </UnitsProvider>
    </BrowserRouter>
  </StrictMode>,
);
