"""New pure guards; no broker/server/native tool or Gradle execution."""
import copy
import os
from pathlib import Path
import unittest
from unittest.mock import patch
import run


def fixture():
    catalog=run.read(run.REPO/'ci/android-native/rich-catalog.json')
    actual=run.read(run.HERE/'original-graph.json')
    actual['metadataContext']='org.gradle.jvm-ecosystem/9.3.1'
    def pom_increment(v):
        # Original accepted raw has exactly 16 POM variants without usage, while
        # both published GMM variants already carry their exact typed usage.
        if 'org.gradle.usage'not in v['attributes']:
            v['attributes'].update({
                'org.gradle.usage':{'type':'java.lang.String','value':'java-runtime'},
                'org.gradle.libraryelements':{'type':'java.lang.String','value':'jar'},
                'org.gradle.category':{'type':'java.lang.String','value':'library'},
            })
    for c in actual['configurations']:
        for a in c['artifacts']:pom_increment(a['variant'])
        for e in c['edges']:pom_increment(e['variant'])
        for component in c['components']:
            for v in component['variants']:pom_increment(v)
    return catalog,actual


class JVMProjection(unittest.TestCase):
    def test_original18_bodies_and_graph_bound_while_variant_output_is_not_trusted_catalog(self):
        catalog,actual=fixture()
        result=run.compare_projection(actual,catalog)
        self.assertEqual(result['artifact_rows'],18)
        self.assertEqual(result['finite_POM_attribute_increments'],16)
        self.assertEqual(result['POM_increment_provenance'],
                         {'observed_first_configuration':12,'source_predicted_second_configuration':4})
        self.assertTrue(result['four_key_variant_identity_exact'])
        self.assertTrue(result['new_variants_are_proposal_only'])
        self.assertFalse(result['trusted_catalog_updated'])
        self.assertEqual(result['raw_display_deltas_pending_Root'],[])
        proposal=copy.deepcopy(actual)
        proposal['configurations'][0]['artifacts'][0]['variant']['display']='configuration runtime'
        result=run.compare_projection(proposal,catalog)
        self.assertEqual(len(result['raw_display_deltas_pending_Root']),1)
        self.assertEqual(result['raw_display_deltas_pending_Root'][0]['after'],'configuration runtime')
        self.assertFalse(result['raw_display_is_production_authority'])
        mutations=[
            ('owner',lambda v:v['owner'].update(version='999')),
            ('capabilities',lambda v:v['capabilities'][0].update(name='foreign')),
            ('external',lambda v:v.update(external={'display':'foreign'})),
            ('original_attribute',lambda v:v['attributes']['org.gradle.status'].update(value='integration')),
            ('increment_type',lambda v:v['attributes']['org.gradle.usage'].update(type='foreign.Type')),
            ('increment_missing',lambda v:v['attributes'].pop('org.gradle.category')),
            ('fourth_attribute',lambda v:v['attributes'].update({'unknown':{'type':'java.lang.String','value':'x'}})),
            ('new_raw_field',lambda v:v.update(unknown='x')),
        ]
        for label,mutate in mutations:
            bad=copy.deepcopy(actual);mutate(bad['configurations'][0]['artifacts'][0]['variant'])
            with self.subTest(mutation=label),self.assertRaises(ValueError):run.compare_projection(bad,catalog)
        for surface in ['component','edge','second_configuration','published_module']:
            bad=copy.deepcopy(actual);c=bad['configurations'][0]
            if surface=='component':v=c['components'][0]['variants'][0]
            elif surface=='edge':v=c['edges'][0]['variant']
            elif surface=='second_configuration':v=bad['configurations'][1]['artifacts'][0]['variant']
            else:v=next(a['variant']for a in c['artifacts']if a['id']['module']=='kotlin-stdlib')
            v['owner']['version']='999'
            with self.subTest(surface=surface),self.assertRaises(ValueError):run.compare_projection(bad,catalog)
        bad=copy.deepcopy(actual)
        gmm=next(a['variant']for a in bad['configurations'][0]['artifacts']if a['id']['module']=='kotlin-stdlib')
        gmm['attributes']['org.gradle.usage']['value']='java-api'
        with self.assertRaises(ValueError):run.compare_projection(bad,catalog)
        bad=copy.deepcopy(actual);bad['configurations'][0]['artifacts'][0]['sha256']='0'*64
        with self.assertRaises(ValueError):run.compare_projection(bad,catalog)

    def test_context_parent_selected_and_forcing_changes_are_rejected(self):
        catalog,actual=fixture()
        for field,value in [('metadataContext','unknown'),('Gradle','9.3.2')]:
            bad=copy.deepcopy(actual);bad[field]=value
            with self.subTest(field=field),self.assertRaises(ValueError):run.compare_projection(bad,catalog)
        for cause in ['FORCED','SELECTED_BY_RULE']:
            bad=copy.deepcopy(actual);bad['configurations'][0]['edges'][0]['selectionReasons']=[{'cause':cause}]
            with self.subTest(cause=cause),self.assertRaises(ValueError):run.compare_projection(bad,catalog)
        bad=copy.deepcopy(actual);bad['configurations'][0]['edges'][0]['selected']['version']='999.0'
        with self.assertRaises(ValueError):run.compare_projection(bad,catalog)

    def test_environment_keeps_real_HOME_and_job_owned_JVM_options_without_ambient_secrets(self):
        before=os.environ['HOME'];work=Path('/tmp/owned-synthetic-jvm-projection')
        with patch.dict(os.environ,{'HTTPS_PROXY':'https://synthetic.invalid','GITHUB_TOKEN':'synthetic-marker'}):
            env=run.environment(work)
        self.assertEqual(env['HOME'],before)
        self.assertEqual(os.environ['HOME'],before)
        self.assertNotIn('HTTPS_PROXY',env);self.assertNotIn('GITHUB_TOKEN',env)
        self.assertEqual(env['GRADLE_USER_HOME'],str(work/'gradle-home'))
        argv=run.command(work,'http://127.0.0.1:12345/maven2/','offline')
        self.assertIn('--offline',argv)
        self.assertTrue(any('-Duser.home='+str(work/'user-home')in str(x)for x in argv))


if __name__=='__main__':unittest.main()
