package store

// All assignment stages are evaluated over one explicit library snapshot.
// Only raw single-parody evidence and human decisions enter known; candidates
// and residual assignments are deliberately never fed back into that CTE.
const tagRelationsCTE = `WITH
 scope AS MATERIALIZED (SELECT e.id,e.work_id,e.library_id,e.revision,e.organization_revision,l.disabled,COALESCE(ef.title,f.filename_title) title,COALESCE(json_extract(ef.fields_json,'$.title_original'),'') title_original,f.file_state
 FROM editions e JOIN libraries l ON l.id=e.library_id JOIN files f ON f.edition_id=e.id LEFT JOIN effective_editions ef ON ef.edition_id=e.id
 WHERE e.library_id=? AND e.trashed_at IS NULL AND l.trashed_at IS NULL),
 scoped_tags AS MATERIALIZED(SELECT et.edition_id,et.tag_id,t.namespace FROM effective_edition_tags et JOIN scope s ON s.id=et.edition_id JOIN tags t ON t.id=et.tag_id),
 parodies AS MATERIALIZED(SELECT st.edition_id,st.tag_id parody_id FROM scoped_tags st JOIN tags t ON t.id=st.tag_id WHERE st.namespace='parody' AND nya_normalize_v1(t.name) NOT IN('original','origin')),
 characters AS MATERIALIZED(SELECT edition_id,tag_id character_id FROM scoped_tags WHERE namespace='character'),
 parody_counts AS(SELECT edition_id,count(*) n FROM parodies GROUP BY edition_id),
 raw_evidence AS MATERIALIZED(SELECT ev.* FROM tag_character_evidence ev JOIN scope s ON s.id=ev.edition_id),
 seeds AS MATERIALIZED(SELECT ev.* FROM raw_evidence ev JOIN scope s ON s.id=ev.edition_id
 WHERE s.disabled=0 AND NOT EXISTS(SELECT 1 FROM parody_character_decisions d WHERE d.parody_id=ev.parody_id AND d.character_id=ev.character_id AND d.decision='excluded')
 AND NOT EXISTS(SELECT 1 FROM edition_character_suppressions x WHERE x.edition_id=ev.edition_id AND x.parody_id=ev.parody_id AND x.character_id=ev.character_id AND x.suppressed=1)),
 known AS MATERIALIZED(SELECT parody_id,character_id FROM seeds UNION SELECT d.parody_id,d.character_id FROM parody_character_decisions d WHERE d.decision='confirmed' AND EXISTS(SELECT 1 FROM characters c WHERE c.character_id=d.character_id)),
 known_counts AS(SELECT character_id,count(*) n FROM known GROUP BY character_id),
 matches AS MATERIALIZED(SELECT c.edition_id,c.character_id,p.parody_id FROM characters c JOIN known k ON k.character_id=c.character_id JOIN parodies p ON p.edition_id=c.edition_id AND p.parody_id=k.parody_id),
 match_counts AS(SELECT edition_id,character_id,count(*) n FROM matches GROUP BY edition_id,character_id),
 initial_pairs AS(SELECT edition_id,parody_id,character_id,'single_parody' basis,1 choices FROM raw_evidence
 UNION ALL SELECT m.edition_id,m.parody_id,m.character_id,'evidence' basis,mc.n choices FROM matches m JOIN match_counts mc ON mc.edition_id=m.edition_id AND mc.character_id=m.character_id JOIN parody_counts pc ON pc.edition_id=m.edition_id AND pc.n>1),
 initial AS MATERIALIZED(SELECT p.*,COALESCE(d.decision,'auto') decision,COALESCE(d.revision,0) decision_revision,COALESCE(x.suppressed,0) suppressed,COALESCE(x.revision,0) suppression_revision,
 CASE WHEN d.decision='excluded' THEN 'excluded' WHEN p.choices>1 THEN 'competing_parodies' WHEN d.decision='confirmed' THEN 'confirmed' WHEN x.suppressed=1 THEN 'suppressed' ELSE p.basis END state
 FROM initial_pairs p LEFT JOIN parody_character_decisions d ON d.parody_id=p.parody_id AND d.character_id=p.character_id LEFT JOIN edition_character_suppressions x ON x.edition_id=p.edition_id AND x.parody_id=p.parody_id AND x.character_id=p.character_id),
 accepted_initial AS MATERIALIZED(SELECT * FROM initial WHERE state IN('single_parody','evidence','confirmed')),
 pending AS MATERIALIZED(SELECT c.edition_id,c.character_id,COALESCE(k.n,0) known_count FROM characters c LEFT JOIN known_counts k ON k.character_id=c.character_id WHERE NOT EXISTS(SELECT 1 FROM accepted_initial a WHERE a.edition_id=c.edition_id AND a.character_id=c.character_id)),
 remaining AS MATERIALIZED(SELECT p.* FROM parodies p WHERE NOT EXISTS(SELECT 1 FROM accepted_initial a WHERE a.edition_id=p.edition_id AND a.parody_id=p.parody_id)),
 residual_pairs AS(SELECT c.edition_id,p.parody_id,c.character_id,'inferred' basis,1 choices FROM pending c JOIN remaining p ON p.edition_id=c.edition_id JOIN parody_counts pc ON pc.edition_id=c.edition_id AND pc.n>1
 WHERE c.known_count=0 AND (SELECT count(*) FROM pending x WHERE x.edition_id=c.edition_id)=1 AND (SELECT count(*) FROM remaining x WHERE x.edition_id=c.edition_id)=1 AND NOT EXISTS(SELECT 1 FROM initial x WHERE x.edition_id=c.edition_id AND x.character_id=c.character_id)),
 residual AS(SELECT p.*,COALESCE(d.decision,'auto') decision,COALESCE(d.revision,0) decision_revision,COALESCE(x.suppressed,0) suppressed,COALESCE(x.revision,0) suppression_revision,
 CASE WHEN d.decision='excluded' THEN 'excluded' WHEN x.suppressed=1 THEN 'suppressed' ELSE 'inferred' END state
 FROM residual_pairs p LEFT JOIN parody_character_decisions d ON d.parody_id=p.parody_id AND d.character_id=p.character_id LEFT JOIN edition_character_suppressions x ON x.edition_id=p.edition_id AND x.parody_id=p.parody_id AND x.character_id=p.character_id),
 blocked_decisions AS(SELECT p.edition_id,p.parody_id,c.character_id,'excluded' basis,1 choices,d.decision,d.revision decision_revision,COALESCE(x.suppressed,0) suppressed,COALESCE(x.revision,0) suppression_revision,'excluded' state
 FROM parodies p JOIN parody_character_decisions d ON d.parody_id=p.parody_id AND d.decision='excluded' JOIN characters c ON c.edition_id=p.edition_id AND c.character_id=d.character_id
 LEFT JOIN edition_character_suppressions x ON x.edition_id=p.edition_id AND x.parody_id=p.parody_id AND x.character_id=c.character_id
 WHERE NOT EXISTS(SELECT 1 FROM initial a WHERE a.edition_id=p.edition_id AND a.parody_id=p.parody_id AND a.character_id=c.character_id) AND NOT EXISTS(SELECT 1 FROM residual a WHERE a.edition_id=p.edition_id AND a.parody_id=p.parody_id AND a.character_id=c.character_id)),
 assignments AS MATERIALIZED(SELECT * FROM initial UNION ALL SELECT * FROM residual UNION ALL SELECT * FROM blocked_decisions),
 accepted AS MATERIALIZED(SELECT * FROM assignments WHERE state IN('single_parody','evidence','confirmed','inferred')),
 used AS(SELECT st.tag_id,count(*) edition_count,count(DISTINCT s.work_id) work_count FROM scoped_tags st JOIN scope s ON s.id=st.edition_id GROUP BY st.tag_id),
 labels AS(SELECT t.id,t.namespace,t.name,COALESCE(m.text,d.translation,r.text,t.name) translation,
 CASE WHEN m.text IS NOT NULL THEN 'manual' WHEN d.translation IS NOT NULL OR r.text IS NOT NULL THEN 'remote' ELSE 'original' END origin,COALESCE(m.revision,0) translation_revision,
 CASE WHEN t.namespace IN('parody','character','artist','group') THEN t.namespace ELSE 'content' END category,
 (t.namespace='parody' AND nya_normalize_v1(t.name) IN('original','origin')) placeholder,COALESCE(u.edition_count,0) edition_count,COALESCE(u.work_count,0) work_count
 FROM tags t LEFT JOIN used u ON u.tag_id=t.id LEFT JOIN tag_translations m ON m.tag_id=t.id AND m.locale='zh-CN' AND m.origin='manual' AND m.dataset=''
 LEFT JOIN dictionary_entries d ON d.generation_id=? AND d.namespace=t.namespace AND d.name=t.name
 LEFT JOIN tag_translations r ON r.tag_id=t.id AND r.locale='zh-CN' AND r.origin='remote' AND r.dataset=(SELECT active_version FROM translation_state WHERE singleton=1) AND ?),
 pairs AS(SELECT parody_id,character_id FROM raw_evidence UNION SELECT parody_id,character_id FROM assignments UNION SELECT parody_id,character_id FROM parody_character_decisions UNION SELECT x.parody_id,x.character_id FROM edition_character_suppressions x JOIN scope s ON s.id=x.edition_id),
 evidence_counts AS(SELECT parody_id,character_id,count(*) n FROM raw_evidence GROUP BY parody_id,character_id),
 seed_counts AS(SELECT parody_id,character_id,count(*) n FROM seeds GROUP BY parody_id,character_id),
 accepted_counts AS(SELECT a.parody_id,a.character_id,count(*) n,count(DISTINCT s.work_id) work_count,sum(a.state='inferred') inferred_count FROM accepted a JOIN scope s ON s.id=a.edition_id GROUP BY a.parody_id,a.character_id),
 suppression_counts AS(SELECT x.parody_id,x.character_id,count(*) n FROM edition_character_suppressions x JOIN scope s ON s.id=x.edition_id WHERE x.suppressed=1 GROUP BY x.parody_id,x.character_id),
 competing_counts AS(SELECT parody_id,character_id,count(*) n FROM assignments WHERE state='competing_parodies' GROUP BY parody_id,character_id),
 pair_counts AS(SELECT p.parody_id,p.character_id,COALESCE(d.decision,'auto') decision,COALESCE(d.revision,0) revision,
 COALESCE(ev.n,0) evidence_count,COALESCE(sc.n,0) seed_count,COALESCE(ac.n,0) edition_count,COALESCE(ac.work_count,0) work_count,COALESCE(ac.inferred_count,0) inferred_count,COALESCE(sp.n,0) suppressed_count,COALESCE(cc.n,0) competing_count
 FROM pairs p LEFT JOIN parody_character_decisions d USING(parody_id,character_id) LEFT JOIN evidence_counts ev USING(parody_id,character_id) LEFT JOIN seed_counts sc USING(parody_id,character_id) LEFT JOIN accepted_counts ac USING(parody_id,character_id) LEFT JOIN suppression_counts sp USING(parody_id,character_id) LEFT JOIN competing_counts cc USING(parody_id,character_id))
`

const catalogTagColumns = "id,namespace,name,translation,origin,translation_revision,category,placeholder,edition_count,work_count"
const unclassifiedPredicate = `(EXISTS(SELECT 1 FROM characters c WHERE c.edition_id=s.id AND NOT EXISTS(SELECT 1 FROM accepted a WHERE a.edition_id=s.id AND a.character_id=c.character_id)) OR NOT EXISTS(SELECT 1 FROM accepted a WHERE a.edition_id=s.id AND (a.parody_id=? OR a.character_id=?)))`
