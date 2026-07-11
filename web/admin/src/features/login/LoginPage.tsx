import { FormEvent, useState } from 'react';
import { KeyRound } from 'lucide-react';
import { Button, Field } from '../../components/ui';
import { useSession } from '../../api/session';

export function LoginPage() {
  const { login } = useSession();
  const [username, setUsername] = useState('admin');
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    setSubmitting(true);
    try {
      // Backend auth is token-only; username exists so browsers can save a login pair.
      await login(token);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="grid min-h-screen place-items-center bg-zinc-100 px-4">
      <form
        action="/admin/api/login"
        autoComplete="on"
        className="grid w-full max-w-sm gap-4 rounded-lg bg-white p-5 shadow-sm"
        method="post"
        onSubmit={submit}
      >
        <div className="flex items-center gap-2">
          <KeyRound size={20} />
          <h1 className="text-lg font-semibold">GuDa Gateway Admin</h1>
        </div>
        <p className="text-sm font-normal text-zinc-600">
          Sign in with any username plus the admin token as the password. Browsers can save this pair for autofill.
        </p>
        <Field
          autoComplete="username"
          autoCapitalize="none"
          autoCorrect="off"
          label="Username"
          name="username"
          onChange={(event) => setUsername(event.target.value)}
          spellCheck={false}
          type="text"
          value={username}
        />
        <Field
          autoComplete="current-password"
          label="Password"
          name="password"
          onChange={(event) => setToken(event.target.value)}
          type="password"
          value={token}
        />
        {error ? <p className="text-sm text-red-600">{error}</p> : null}
        <Button disabled={!token.trim() || submitting} type="submit">
          Log in
        </Button>
      </form>
    </main>
  );
}
