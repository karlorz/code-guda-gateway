import { FormEvent, useState } from 'react';
import { KeyRound } from 'lucide-react';
import { Button, Field } from '../../components/ui';
import { useSession } from '../../api/session';

export function LoginPage() {
  const { login } = useSession();
  const [token, setToken] = useState('');
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await login(token);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed');
    }
  }

  return (
    <main className="grid min-h-screen place-items-center bg-zinc-100 px-4">
      <form className="grid w-full max-w-sm gap-4 rounded-lg bg-white p-5 shadow-sm" onSubmit={submit}>
        <div className="flex items-center gap-2">
          <KeyRound size={20} />
          <h1 className="text-lg font-semibold">GuDa Gateway Admin</h1>
        </div>
        <Field label="Admin token" onChange={(event) => setToken(event.target.value)} type="password" value={token} />
        {error ? <p className="text-sm text-red-600">{error}</p> : null}
        <Button disabled={!token.trim()} type="submit">
          Log in
        </Button>
      </form>
    </main>
  );
}
