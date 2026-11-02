/** Text that means a site is broken even though it answers 200 OK, so the status-code check alone would call it online. */
export const PROBLEM_PRESETS: { text: string; why: string }[] = [
  { text: 'Error establishing a database connection', why: 'WordPress cannot reach its database' },
  { text: 'There has been a critical error on this website', why: 'WordPress has crashed' },
  { text: 'Fatal error', why: 'a PHP error printed on the page' },
  { text: 'Account Suspended', why: 'the hosting account is suspended' },
  { text: 'Welcome to nginx!', why: 'the server’s default page: the site’s files are missing' },
  { text: 'It works!', why: 'Apache’s default page: the site’s files are missing' },
  { text: 'Index of /', why: 'a list of files is shown instead of the site' },
  { text: 'is for sale', why: 'the domain has expired and been parked' },
]
