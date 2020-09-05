begin;
	alter table hits set unlogged;

	create index tmp on hits(browser);
	update hits set
		path_id=(select path_id from paths where paths.site_id=hits.site and lower(paths.path)=lower(hits.path)),
		user_agent_id=(select user_agent_id from user_agents where ua=hits.browser);
	drop index tmp;

	update hits set
		session2=cast(rpad(cast(session as varchar), 16, '0') as bytea)
		where session is not null;

	alter table hits set logged;

	insert into version values('2020-08-28-2-paths-paths');
commit;
